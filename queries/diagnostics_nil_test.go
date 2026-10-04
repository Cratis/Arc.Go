// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/queries"
)

//nolint:staticcheck // Intentionally exercise nil-context rejection and preserve error precedence.
func TestDiagnosticsNilContextHasOneRejectingQueryBoundary(t *testing.T) {
	for _, scenario := range []string{"typed", "typed mismatch", "unknown", "scoped", "nil scope", "open"} {
		t.Run(scenario, func(t *testing.T) {
			var results []queries.Details
			var failures []string
			for _, enabled := range []bool{false, true} {
				var recorder *observability.Recorder
				if enabled {
					recorder = diagnosticRecorder(t)
				}
				var registry queries.Registry
				mustRegister(t, queries.Register[Item](&registry, "Current", queries.Function(func(context.Context, queries.NoArguments) (Item, error) { t.Fatal("executed"); return Item{}, nil })))
				p := build(t, &registry, queries.PipelineOptions{Diagnostics: recorder})
				scope, err := execution.OpenScope(t.Context(), nil)
				mustRegister(t, err)
				var details queries.Details
				switch scenario {
				case "typed":
					r, e := queries.Perform[Item](nil, p, "Item.Current", queries.Request{})
					details, err = r.Details(), e
				case "typed mismatch":
					r, e := queries.Perform[string](nil, p, "Item.Current", queries.Request{})
					details, err = r.Details(), e
				case "unknown":
					r, e := queries.Perform[Item](nil, p, "SECRET unknown", queries.Request{})
					details, err = r.Details(), e
				case "scoped":
					r, e := p.PerformScoped(nil, scope, "Item.Current", queries.Request{})
					details, err = r.Details(), e
				case "nil scope":
					r, e := p.PerformScoped(nil, nil, "Item.Current", queries.Request{})
					details, err = r.Details(), e
				case "open":
					_, r, e := p.(queries.ObservablePipeline).Open(nil, "Item.Current", queries.Request{})
					details, err = r.Details(), e
				}
				if err == nil {
					t.Fatal("nil context succeeded")
				}
				results, failures = append(results, details), append(failures, err.Error())
				mustRegister(t, scope.Close(t.Context()))
				if enabled {
					s := recorder.Snapshot()
					if len(s.Events) != 1 || len(s.Metrics) != 1 || s.Metrics[0].Count != 1 {
						t.Fatal(s)
					}
				}
			}
			if !reflect.DeepEqual(results[0], results[1]) || failures[0] != failures[1] {
				t.Fatal(results, failures)
			}
		})
	}
}
