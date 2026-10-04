// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/queries"
)

const healthPath = "/.cratis/queries/health"

func healthAuthentication(_ context.Context, r *http.Request) (authentication.Result, error) {
	role := r.Header.Get("X-Test-Role")
	if role == "" {
		return authentication.Anonymous(), nil
	}
	return authentication.Authenticated(identity.NewPrincipal(identity.PrincipalData{ID: "SECRET subject", Name: "SECRET name", AuthenticationType: "test", Roles: []string{role}}))
}

func TestQueryHealthIsAbsentWithoutExplicitOptIn(t *testing.T) {
	for _, environment := range []string{"Production", "Development"} {
		t.Run(environment, func(t *testing.T) {
			b, err := arc.NewBuilder(arc.Options{Environment: environment})
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Handle("/", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("reserved path reached raw fallback") })); err != nil {
				t.Fatal(err)
			}
			a, err := buildStarted(t, b)
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range a.Endpoints() {
				if endpoint.Path == healthPath {
					t.Fatal(endpoint)
				}
			}
			if _, exists := a.Queries().Lookup(arc.QueryHealthName); exists {
				t.Fatal("health registered by default")
			}
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest("GET", healthPath, nil))
			if w.Code != 404 {
				t.Fatal(w.Code)
			}
		})
	}
	for _, roles := range [][]string{nil, {}, {""}, {" "}, {" admin"}} {
		if _, err := arc.NewBuilder(arc.Options{QueryHealth: &arc.QueryHealthOptions{Roles: roles}}); err == nil {
			t.Fatal("invalid health roles accepted", roles)
		}
	}
}

type forbiddenHealthReader struct{}

func (forbiddenHealthReader) Method() string               { return "GET" }
func (forbiddenHealthReader) ResponseCacheControl() string { return "" }
func (forbiddenHealthReader) Read(context.Context, queries.ReaderInput) (queries.Request, error) {
	panic("application reader must not run for health")
}

func TestQueryHealthRolesAreCopiedAndPrivatePipelineBypassesApplicationFactories(t *testing.T) {
	options := &arc.QueryHealthOptions{Roles: []string{"diagnostics"}}
	b, err := arc.NewBuilder(arc.Options{QueryHealth: options, Authentication: []authentication.Handler{authentication.HandlerFunc(healthAuthentication)}, HTTP: arc.HTTPOptions{QueryReaders: []queries.RequestReader{forbiddenHealthReader{}}}, OpenResources: func(context.Context) (execution.Resources, error) {
		t.Fatal("application resources opened")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	options.Roles[0] = "mutated"
	if err := b.Queries().AddFilter("application", func(context.Context, *execution.Scope) (queries.Filter, error) {
		t.Fatal("application filter activated")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Queries().AddEmissionGuard("application", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		t.Fatal("application guard activated")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"", "wrong", "mutated", "diagnostics"} {
		ctx := identity.WithPrincipal(t.Context(), identity.System(role))
		if role == "" {
			ctx = t.Context()
		}
		result, err := queries.Perform[arc.QueryHealth](ctx, a.Queries(), arc.QueryHealthName, queries.Request{})
		if err != nil || result.IsAuthorized() != (role == "diagnostics") {
			t.Fatal(role, result, err)
		}
		if role == "diagnostics" {
			value, present := result.Data()
			if !present || len(value.Observations) != 0 || len(value.Hubs) != 2 {
				t.Fatal(value, present)
			}
		}
		scope, err := execution.OpenScope(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		scoped, err := a.Queries().PerformScoped(ctx, scope, arc.QueryHealthName, queries.Request{})
		if err != nil || scoped.IsAuthorized() != (role == "diagnostics") {
			t.Fatal(role, scoped, err)
		}
		if err := scope.Close(ctx); err != nil {
			t.Fatal(err)
		}
		o, admission, err := a.Queries().(queries.ObservablePipeline).Open(ctx, arc.QueryHealthName, queries.Request{})
		if err != nil || admission.IsAuthorized() != (role == "diagnostics") || (o != nil) != (role == "diagnostics") {
			t.Fatal(role, o, admission, err)
		}
		if o != nil {
			// Rebinding an observation to a different principal cannot expose a sample.
			err := o.Run(identity.WithPrincipal(ctx, identity.System("wrong")), queries.ObservationOptions{}, func(queries.Result[any]) error { t.Fatal("changed identity emitted"); return nil })
			if !errors.Is(err, execution.ErrIdentityChanged) {
				t.Fatal(err)
			}
			if err := o.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		for _, method := range []string{"GET", "HEAD", "QUERY"} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, healthPath, strings.NewReader("{}"))
			r.Header.Set("X-Test-Role", role)
			a.ServeHTTP(w, r)
			want := 403
			if role == "diagnostics" {
				want = 200
				if method == "HEAD" {
					want = 202
				}
			}
			if w.Code != want || !strings.Contains(w.Header().Get("Cache-Control"), "private") || strings.Contains(w.Body.String(), "SECRET") {
				t.Fatal(role, method, w.Code, w.Header(), w.Body.String())
			}
		}
	}
}

func TestQueryHealthSamplingUsesConsumerLifetimeAndExcludesItself(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, err := arc.NewBuilder(arc.Options{QueryHealth: &arc.QueryHealthOptions{Roles: []string{"diagnostics"}}})
		if err != nil {
			t.Fatal(err)
		}
		a, err := buildStarted(t, b)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(identity.WithPrincipal(t.Context(), identity.System("diagnostics")))
		defer cancel()
		o, _, err := a.Queries().(queries.ObservablePipeline).Open(ctx, arc.QueryHealthName, queries.Request{})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		started := time.Now()
		err = o.Run(ctx, queries.ObservationOptions{}, func(result queries.Result[any]) error {
			value, present := result.Data()
			if !present || len(value.(arc.QueryHealth).Observations) != 0 {
				t.Fatal(value)
			}
			count++
			if count == 3 {
				cancel()
			}
			return nil
		})
		if !errors.Is(err, context.Canceled) || count != 3 || time.Since(started) != 2*time.Second {
			t.Fatal(err, count, time.Since(started))
		}
		if err := a.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := o.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQueryHealthHubSubscriptionsEnforceRolesAndExposeOnlyAggregates(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{QueryHealth: &arc.QueryHealthOptions{Roles: []string{"diagnostics"}}, Authentication: []authentication.Handler{authentication.HandlerFunc(healthAuthentication)}, OpenResources: func(context.Context) (execution.Resources, error) {
		t.Error("application resource activated")
		return nil, errors.New("SECRET resource")
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	for _, role := range []string{"", "wrong", "diagnostics"} {
		headers := http.Header{"X-Test-Role": []string{role}}
		client := openTestHub(t, server, headers)
		want := 401
		if role == "diagnostics" {
			want = 200
		}
		client.control(t, "/subscribe", "SECRET-control", 1, map[string]any{"queryName": string(arc.QueryHealthName), "transferMode": "full"}, headers, want)
		message := readSSEResult(t, client.reader)
		if role != "diagnostics" {
			if message["type"] != "Unauthorized" {
				t.Fatal(message)
			}
		} else {
			if message["type"] != "QueryResult" {
				t.Fatal(message)
			}
			data := message["payload"].(map[string]any)["data"]
			body, err := json.Marshal(data)
			if err != nil || strings.Contains(string(body), "SECRET") || strings.Contains(string(body), client.id) {
				t.Fatal(string(body), err)
			}
			client.control(t, "/unsubscribe", "SECRET-control", 1, nil, headers, 200)
		}
		if err := client.response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
