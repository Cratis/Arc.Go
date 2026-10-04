// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/validation"
)

//nolint:staticcheck // Intentionally exercise nil-context rejection and preserve error precedence.
func TestDiagnosticsNilContextHasOneRejectingCommandBoundary(t *testing.T) {
	for _, scenario := range []string{"typed", "typed mismatch", "typed invalid severity", "scoped", "validate scoped", "nil scope"} {
		t.Run(scenario, func(t *testing.T) {
			var results []commands.Details
			var failures []string
			for _, enabled := range []bool{false, true} {
				var recorder *observability.Recorder
				if enabled {
					recorder = diagnosticRecorder(t)
				}
				var registry commands.Registry
				must(t, commands.Register[Clear](&registry, commands.Handle(func(Clear, context.Context) (string, error) { t.Fatal("executed"); return "", nil })))
				p := build(t, &registry, commands.PipelineOptions{Diagnostics: recorder})
				scope, err := execution.OpenScope(t.Context(), nil)
				must(t, err)
				var details commands.Details
				switch scenario {
				case "typed":
					r, e := commands.Execute[string](nil, p, Clear{})
					details, err = r.Details(), e
				case "typed mismatch":
					r, e := commands.Execute[int](nil, p, Clear{})
					details, err = r.Details(), e
				case "typed invalid severity":
					severity := validation.Severity(255)
					r, e := commands.Execute[string](nil, p, Clear{}, commands.ExecuteOptions{AllowedSeverity: &severity})
					details, err = r.Details(), e
				case "scoped":
					r, e := p.ExecuteScoped(nil, scope, Clear{})
					details, err = r.Details(), e
				case "validate scoped":
					r, e := p.ValidateScoped(nil, scope, Clear{})
					details, err = r.Details(), e
				case "nil scope":
					r, e := p.ExecuteScoped(nil, nil, Clear{})
					details, err = r.Details(), e
				}
				if err == nil {
					t.Fatal("nil context succeeded")
				}
				results, failures = append(results, details), append(failures, err.Error())
				must(t, scope.Close(t.Context()))
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
