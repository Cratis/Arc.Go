// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
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
func (a *Application) serveIngress(w http.ResponseWriter, r *http.Request) {
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
	ctx := correlation.WithID(r.Context(), id)
	var received time.Time
	if err := boundary.Call(ctx, func(context.Context) error { received = a.options.Clock(); return nil }); err != nil {
		w.WriteHeader(500)
		return
	}
	ctx = boundary.ForwardReceipt(ctx, received)
	work, release, err := a.admit(ctx)
	if err != nil {
		w.WriteHeader(503)
		return
	}
	defer release()
	ctx = work
	r = r.WithContext(ctx)
	if !canonicalPath(r) {
		w.WriteHeader(400)
		return
	}
	methods, known := a.routeTable[r.URL.Path]
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
	if known && !matched {
		a.handler.ServeHTTP(w, r)
		return
	}
	// Unknown-route outcomes are not authentication challenges.
	if !known {
		a.handler.ServeHTTP(w, r)
		return
	}
	chain, _ := authentication.New(a.options.Authentication...)
	var result authentication.Result
	err = boundary.Call(ctx, func(ctx context.Context) error { var err error; result, err = chain.Authenticate(ctx, r); return err })
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
		err = &queries.ReadError{Cause: err}
	}
	a.publish(w, r, status, queries.FromError[any](id, err))
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
