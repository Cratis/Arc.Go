// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
)

func headerValues(h http.Header, name string) []string {
	var values []string
	for key, v := range h {
		if strings.EqualFold(key, name) {
			values = append(values, v...)
		}
	}
	return values
}
func (a *Application) serveIngress(w http.ResponseWriter, r *http.Request, observed *responseWriter) {
	text := ""
	if values := headerValues(r.Header, a.options.HTTP.CorrelationHeader); len(values) == 1 {
		text = values[0]
	}
	id, err := correlation.Resolve(r.Context(), text)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	w.Header().Set(a.options.HTTP.CorrelationHeader, id.String())
	a.prepareHeaders(w, r)
	ctx := correlation.WithID(r.Context(), id)
	var received time.Time
	if err := boundary.Call(ctx, func(context.Context) error { received = a.options.Clock(); return nil }); err != nil {
		w.WriteHeader(500)
		return
	}
	ctx = execution.WithReceivedAt(context.WithValue(ctx, httpReceiptKey{}, received), received)
	work, release, err := a.admit(ctx)
	if err != nil {
		w.WriteHeader(503)
		return
	}
	defer release()
	ctx = work
	r = r.WithContext(ctx)
	defer func() {
		if value := recover(); value != nil {
			if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(http.ErrAbortHandler)
			}
			err := &execution.PanicError{Value: value, Stack: debug.Stack()}
			if observed.status != 0 || observed.hijacked {
				a.hostFailure(ctx, "HTTP ingress panicked after publication", err)
				return
			}
			e, matched := a.routeTable[r.URL.Path][r.Method]
			if !matched {
				e = metadata.Endpoint{Identity: "/.cratis/raw"}
			}
			w.Header().Del("Content-Length")
			w.Header().Del("Content-Type")
			a.ingressFailure(w, r, e, err, 500)
		}
	}()
	if !canonicalPath(r) {
		w.WriteHeader(400)
		return
	}
	a.requestHandler.ServeHTTP(w, r)
}

type httpReceiptKey struct{}

// httpPipelineContext installs forwarding only for the immediate endpoint call.
// The ingress timestamp is ordinary metadata everywhere else, not a capability
// to reuse that timestamp for unrelated backend operations.
func httpPipelineContext(ctx context.Context) context.Context {
	received, ok := ctx.Value(httpReceiptKey{}).(time.Time)
	ctx = context.WithValue(ctx, httpReceiptKey{}, nil)
	if ok {
		return boundary.ForwardReceipt(ctx, received)
	}
	return ctx
}

func (a *Application) prepareHeaders(w http.ResponseWriter, r *http.Request) {
	methods := a.routeTable[r.URL.Path]
	e, matched := methods[r.Method]
	if matched && r.Method == "QUERY" {
		w.Header().Set("Cache-Control", "no-store")
	}
	if matched && strings.HasPrefix(e.Identity, "/.cratis/") {
		privateCache(w)
		if e.Path == "/.cratis/me" {
			expireLegacyCookie(w, r)
		}
	}
}
func (a *Application) authenticateAndDispatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	methods, known := a.routeTable[r.URL.Path]
	e, matched := methods[r.Method]
	if known && !matched {
		a.handler.ServeHTTP(w, r)
		return
	}
	// Unknown-route outcomes are not authentication challenges.
	if !known {
		if reservedPath(r.URL.Path) {
			a.handler.ServeHTTP(w, r)
			return
		}
		if _, pattern := a.rawMux.Handler(r); pattern == "" {
			a.handler.ServeHTTP(w, r)
			return
		}
		e = metadata.Endpoint{Identity: "/.cratis/raw"}
	}
	var result authentication.Result
	err := boundary.Call(ctx, func(ctx context.Context) error {
		var err error
		result, err = a.authentication.Authenticate(ctx, r)
		return err
	})
	if err != nil {
		a.ingressFailure(w, r, e, err, 500)
		return
	}
	if result.Failure() != nil {
		a.ingressFailure(w, r, e, nil, 401)
		return
	}
	principal, _ := result.Principal()
	ctx = identity.WithPrincipal(ctx, principal)
	var tenant tenancy.ID
	err = boundary.Call(ctx, func(ctx context.Context) error {
		var err error
		tenant, err = a.options.TenantResolver.Resolve(ctx, r.WithContext(ctx))
		return err
	})
	if err != nil {
		status := 500
		if errors.Is(err, tenancy.ErrInvalidID) || errors.Is(err, tenancy.ErrAmbiguousSelection) || errors.Is(err, tenancy.ErrUnsupportedHost) {
			status = 400
		}
		a.ingressFailure(w, r, e, err, status)
		return
	}
	ctx = tenancy.WithTenant(ctx, tenant)
	a.handler.ServeHTTP(w, r.WithContext(ctx))
}
func (a *Application) ingressFailure(w http.ResponseWriter, r *http.Request, e metadata.Endpoint, err error, status int) {
	if err != nil {
		level := slog.LevelError
		if status < 500 {
			level = slog.LevelWarn
		}
		a.hostFailure(r.Context(), "HTTP ingress failed", err, level)
	}
	if strings.HasPrefix(e.Identity, "/.cratis/") {
		w.WriteHeader(status)
		return
	}
	id := correlation.FromContext(r.Context())
	if e.Method == "POST" {
		if status == 401 {
			a.publish(w, r, status, commands.Unauthorized(id, ""))
			return
		}
		if status == 400 {
			a.publish(w, r, status, commands.InvalidBody(id))
			return
		}
		a.publish(w, r, status, commands.FromError[any](id, err))
		return
	}
	if status == 401 {
		a.publish(w, r, status, queries.Unauthorized[any](id, ""))
		return
	}
	if status == 400 {
		err = &ingressReadError{ReadError: &queries.ReadError{Malformed: true, Cause: err}}
	}
	a.publish(w, r, status, queries.FromError[any](id, err))
}

// ingressReadError classifies invalid tenant selection as caller validation.
// Reader syntax exceptions retain their existing C# exception envelope.
type ingressReadError struct{ *queries.ReadError }

func (e *ingressReadError) ValidationResults() []validation.Result {
	return []validation.Result{{Severity: validation.Error, Message: "The request tenant selection is malformed.", Reason: "malformedRequest"}}
}

func (a *Application) publish(w http.ResponseWriter, r *http.Request, status int, value any) {
	body, err := encode(r.Context(), value)
	if err != nil || int64(len(body)) > a.options.HTTP.MaxResponseBytes {
		value, status = publicationFailure(value)
		body, err = encode(r.Context(), value)
		if err != nil || value == nil || int64(len(body)) > a.options.HTTP.MaxResponseBytes {
			w.WriteHeader(500)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		if _, err := w.Write(body); err != nil && a.options.Logger != nil {
			a.options.Logger.DebugContext(r.Context(), "HTTP publication interrupted", "method", r.Method, "correlationId", correlation.FromContext(r.Context()))
		}
	}
}
func privateCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, private")
	tokens := strings.Join(w.Header().Values("Vary"), ",")
	for _, token := range strings.Split(tokens, ",") {
		if strings.EqualFold(strings.TrimSpace(token), "Cookie") || strings.TrimSpace(token) == "*" {
			return
		}
	}
	if tokens != "" {
		tokens += ", "
	}
	w.Header().Set("Vary", tokens+"Cookie")
}
func expireLegacyCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(".cratis-identity"); err == nil {
		http.SetCookie(w, &http.Cookie{Name: ".cratis-identity", Path: "/", Value: "", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	}
}
