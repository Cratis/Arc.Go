// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/queries"
)

func TestHTTPDiagnosticsPreDispatchFailuresAndNestedAuthenticationAreIndependent(t *testing.T) {
	for _, scenario := range []struct {
		name, method, path, body string
		status                   int
		outcome                  observability.Outcome
	}{
		{"auth denial", "POST", "/api/builder-command", "{}", 401, observability.Authorization},
		{"auth panic", "GET", "/diag-query", "", 500, observability.Error},
		{"command decode", "POST", "/api/builder-command", "SECRET bad", 400, observability.Validation},
		{"query reader", "GET", "/diag-query?x=%zz", "", 400, observability.Error},
		{"success", "GET", "/diag-query", "", 200, observability.Success},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			recorder, err := observability.NewRecorder(observability.Options{})
			if err != nil {
				t.Fatal(err)
			}
			var application *arc.Application
			b, err := arc.NewBuilder(arc.Options{Diagnostics: recorder, Authentication: []authentication.Handler{authentication.HandlerFunc(func(ctx context.Context, _ *http.Request) (authentication.Result, error) {
				_, err := commands.Execute[commands.NoResponse](ctx, application.Commands(), builderCommand{})
				if err != nil {
					return authentication.Result{}, err
				}
				switch scenario.name {
				case "auth denial":
					return authentication.Failed("SECRET credentials"), nil
				case "auth panic":
					panic("SECRET auth panic")
				}
				return authentication.Anonymous(), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			if err := commands.Register[builderCommand](b); err != nil {
				t.Fatal(err)
			}
			if err := queries.Register[builderModel](b, "Current", queries.Function(func(context.Context, queries.NoArguments) (builderModel, error) {
				return builderModel{Name: "SECRET data"}, nil
			}), queries.WithPath[queries.NoArguments]("/diag-query")); err != nil {
				t.Fatal(err)
			}
			application, err = buildStarted(t, b)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(scenario.method, scenario.path, strings.NewReader(scenario.body))
			w := httptest.NewRecorder()
			application.ServeHTTP(w, r)
			if w.Code != scenario.status {
				t.Fatal(w.Code, w.Body.String())
			}
			s := recorder.Snapshot()
			if len(s.Events) != 2 || s.Events[0].Operation != observability.Command || s.Events[0].Outcome != observability.Success || s.Events[1].Outcome != scenario.outcome {
				t.Fatal(s)
			}
			var count uint64
			for _, metric := range s.Metrics {
				count += metric.Count
			}
			if count != 2 {
				t.Fatal(count)
			}
			body, err := json.Marshal(s)
			if err != nil || strings.Contains(string(body), "SECRET") {
				t.Fatal(string(body), err)
			}
		})
	}
}

//nolint:staticcheck // Intentionally exercise nil-context rejection, never replacement with Background.
func TestAdmittedDiagnosticsNilContextDoesNotChangeErrorsOrDoubleCount(t *testing.T) {
	for _, scenario := range []string{"command", "query", "command scoped", "query scoped", "validate scoped", "open"} {
		t.Run(scenario, func(t *testing.T) {
			var outputs []any
			var failures []string
			for _, enabled := range []bool{false, true} {
				var recorder *observability.Recorder
				if enabled {
					recorder, _ = observability.NewRecorder(observability.Options{})
				}
				b, err := arc.NewBuilder(arc.Options{Diagnostics: recorder, OpenResources: func(context.Context) (execution.Resources, error) { t.Fatal("resources activated"); return nil, nil }})
				if err != nil {
					t.Fatal(err)
				}
				if err := commands.Register[builderCommand](b); err != nil {
					t.Fatal(err)
				}
				if err := queries.Register[builderModel](b, "Current", queries.Function(func(context.Context, queries.NoArguments) (builderModel, error) {
					t.Fatal("query activated")
					return builderModel{}, nil
				})); err != nil {
					t.Fatal(err)
				}
				app, err := buildStarted(t, b)
				if err != nil {
					t.Fatal(err)
				}
				var details any
				switch scenario {
				case "command":
					r, e := commands.Execute[commands.NoResponse](nil, app.Commands(), builderCommand{})
					details, err = r.Details(), e
				case "query":
					r, e := queries.Perform[builderModel](nil, app.Queries(), "builderModel.Current", queries.Request{})
					details, err = r.Details(), e
				case "command scoped":
					r, e := app.Commands().ExecuteScoped(nil, nil, builderCommand{})
					details, err = r.Details(), e
				case "query scoped":
					r, e := app.Queries().PerformScoped(nil, nil, "builderModel.Current", queries.Request{})
					details, err = r.Details(), e
				case "validate scoped":
					r, e := app.Commands().ValidateScoped(nil, nil, builderCommand{})
					details, err = r.Details(), e
				case "open":
					_, r, e := app.Queries().(queries.ObservablePipeline).Open(nil, "builderModel.Current", queries.Request{})
					details, err = r.Details(), e
				}
				if !errors.Is(err, arc.ErrInvalidOptions) {
					t.Fatal(err)
				}
				outputs, failures = append(outputs, details), append(failures, err.Error())
				if enabled {
					s := recorder.Snapshot()
					if len(s.Events) != 1 || len(s.Metrics) != 1 || s.Metrics[0].Count != 1 {
						t.Fatal(s)
					}
				}
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) || failures[0] != failures[1] {
				t.Fatal(outputs, failures)
			}
		})
	}
}
