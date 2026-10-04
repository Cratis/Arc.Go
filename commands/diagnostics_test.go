// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/validation"
)

func diagnosticRecorder(t *testing.T) *observability.Recorder {
	t.Helper()
	r, err := observability.NewRecorder(observability.Options{})
	must(t, err)
	return r
}

func TestDiagnosticsPreserveCommandResultsCallbacksAndClock(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cleanup error"}[failure], func(t *testing.T) {
			var outputs []commands.Result[any]
			var failures []bool
			for _, enabled := range []bool{false, true} {
				var recorder *observability.Recorder
				if enabled {
					recorder = diagnosticRecorder(t)
				}
				var registry commands.Registry
				calls, clocks, opens := 0, 0, 0
				cause := errors.New("SECRET dependency")
				resources := &resource{}
				if failure {
					resources.closeErr = cause
				}
				must(t, commands.Register[Clear](&registry, commands.Handle(func(Clear, context.Context) (string, error) { calls++; return "SECRET response", nil })))
				p := build(t, &registry, commands.PipelineOptions{Diagnostics: recorder, Clock: func() time.Time { clocks++; return time.Unix(1, 0) }, OpenResources: func(context.Context) (execution.Resources, error) { opens++; return resources, nil }})
				if calls != 0 || clocks != 0 || opens != 0 {
					t.Fatal("build activated services")
				}
				id, err := correlation.Resolve(t.Context(), "12345678-1234-1234-1234-123456789abc")
				must(t, err)
				result, err := p.Execute(correlation.WithID(t.Context(), id), Clear{})
				outputs = append(outputs, result)
				failures = append(failures, errors.Is(err, cause))
				if calls != 1 || clocks != 1 || opens != 1 || resources.closed != 1 {
					t.Fatal(calls, clocks, opens, resources.closed)
				}
				if enabled {
					s := recorder.Snapshot()
					want := observability.Success
					if failure {
						want = observability.Error
					}
					if len(s.Events) != 1 || s.Events[0].Outcome != want || s.Metrics[0].Count != 1 {
						t.Fatal(s)
					}
					body, err := json.Marshal(s)
					must(t, err)
					if strings.Contains(string(body), "SECRET") {
						t.Fatal(string(body))
					}
				}
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) || failures[0] != failures[1] {
				t.Fatal("diagnostics changed outcome", outputs, failures)
			}
		})
	}
}

func TestDiagnosticsCountTypedScopedValidateAndCancelledAttemptsOnce(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var registry commands.Registry
	calls := 0
	must(t, commands.Register[Clear](&registry, commands.Handle(func(Clear, context.Context) (string, error) { calls++; return "ok", nil })))
	p := build(t, &registry, commands.PipelineOptions{Diagnostics: recorder})
	_, err := commands.Execute[int](t.Context(), p, Clear{})
	if !errors.Is(err, commands.ErrResponseType) {
		t.Fatal(err)
	}
	_, err = p.ExecuteScoped(t.Context(), nil, Clear{})
	if !errors.Is(err, execution.ErrInvalidScope) {
		t.Fatal(err)
	}
	_, err = p.ValidateScoped(t.Context(), nil, Clear{})
	if !errors.Is(err, execution.ErrInvalidScope) {
		t.Fatal(err)
	}
	_, err = p.Validate(t.Context(), Clear{})
	must(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = commands.Execute[string](ctx, p, Clear{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = commands.Execute[string](t.Context(), p, Clear{})
	must(t, err)
	s := recorder.Snapshot()
	want := []observability.Outcome{observability.Error, observability.Error, observability.Error, observability.Success, observability.Cancelled, observability.Success}
	if calls != 1 || len(s.Events) != len(want) {
		t.Fatal(calls, s)
	}
	for i, outcome := range want {
		if s.Events[i].Outcome != outcome {
			t.Fatal(i, s.Events[i])
		}
	}
	if s.Events[2].Operation != observability.Validate || s.Events[3].Operation != observability.Validate {
		t.Fatal(s)
	}
}

func TestDiagnosticsNestedCommandsReceiveFreshAttempts(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var registry commands.Registry
	var p commands.Pipeline
	clocks := 0
	must(t, commands.Register[Clear](&registry, commands.Handle(func(_ Clear, ctx context.Context) (string, error) {
		result, err := commands.Execute[string](ctx, p, Rename{"nested SECRET"})
		response, _ := result.Response()
		return response, err
	})))
	must(t, commands.Register(&registry, commands.Handle(Rename.Handle)))
	p = build(t, &registry, commands.PipelineOptions{Diagnostics: recorder, Clock: func() time.Time { clocks++; return time.Unix(1, 0) }})
	_, err := commands.Execute[string](t.Context(), p, Clear{})
	must(t, err)
	s := recorder.Snapshot()
	if len(s.Events) != 2 || s.Events[0].Artifact == s.Events[1].Artifact || clocks != 2 {
		t.Fatal(s, clocks)
	}
}

type diagnosticAppendError struct{}

func (diagnosticAppendError) Error() string { return "SECRET append error" }
func (diagnosticAppendError) ValidationResults() []validation.Result {
	return []validation.Result{{Severity: validation.Error, Reason: validation.ConstraintViolation, Message: "SECRET constraint"}}
}

func TestDiagnosticsAppendFailureErrorUsesFinalizedValidationOutcome(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var registry commands.Registry
	must(t, commands.Register[Clear](&registry, commands.Void(func(Clear, context.Context) error { return diagnosticAppendError{} })))
	p := build(t, &registry, commands.PipelineOptions{Diagnostics: recorder})
	result, err := p.Execute(t.Context(), Clear{})
	if err == nil || result.IsValid() || result.HasExceptions() {
		t.Fatal(result, err)
	}
	s := recorder.Snapshot()
	if len(s.Events) != 1 || s.Events[0].Outcome != observability.AppendRejected {
		t.Fatal(s)
	}
}

func TestDiagnosticsClassifyAppendRejectionWithoutRetainingValidationState(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var registry commands.Registry
	must(t, commands.Register[Clear](&registry, commands.WithValidator(validation.ValidatorFunc[Clear](func(context.Context, Clear) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Error, Message: "SECRET finding", Reason: validation.ConstraintViolation, State: struct{ Secret string }{"SECRET state"}}}, nil
	}))))
	p := build(t, &registry, commands.PipelineOptions{Diagnostics: recorder})
	_, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	s := recorder.Snapshot()
	if len(s.Events) != 1 || s.Events[0].Outcome != observability.AppendRejected {
		t.Fatal(s)
	}
	body, err := json.Marshal(s)
	must(t, err)
	if strings.Contains(string(body), "SECRET") {
		t.Fatal(string(body))
	}
}
