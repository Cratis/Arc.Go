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

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

type terminalParticipant struct {
	begin    func(context.Context, *commands.Invocation) error
	complete func(context.Context, *commands.Invocation, commands.Result[any]) (commands.CompletionReport, error)
}

func (p terminalParticipant) Begin(ctx context.Context, inv *commands.Invocation) error {
	return p.begin(ctx, inv)
}
func (p terminalParticipant) Complete(ctx context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
	return p.complete(ctx, inv, result)
}

func TestTerminalCompletionOrderingAndFailurePreventsCommit(t *testing.T) {
	for _, terminalFirst := range []bool{true, false} {
		for _, fail := range []string{"", "begin", "complete", "nested"} {
			t.Run(fail+map[bool]string{true: "-first", false: "-last"}[terminalFirst], func(t *testing.T) {
				var r commands.Registry
				var order []string
				cause := errors.New("ordinary failure")
				commits := 0
				addTerminal := func() {
					must(t, r.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
						return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { order = append(order, "reserve"); return nil }, complete: func(ctx context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
							order = append(order, "terminal")
							if ctx.Err() != nil {
								t.Fatal(ctx.Err())
							}
							if result.IsSuccess() {
								commits++
								return commands.CompletionReport{Disposition: commands.Committed}, nil
							}
							return commands.CompletionReport{Disposition: commands.NotCommitted}, nil
						}}, nil
					}))
				}
				if terminalFirst {
					addTerminal()
				}
				must(t, r.AddExecutionScope("ordinary", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
					return participant{begin: func(context.Context, *commands.Invocation) error {
						order = append(order, "begin")
						if fail == "begin" {
							return cause
						}
						return nil
					}, complete: func(_ context.Context, inv *commands.Invocation, _ commands.Result[any]) (commands.Result[commands.NoResponse], error) {
						order = append(order, "ordinary")
						if fail == "complete" {
							return commands.Result[commands.NoResponse]{}, cause
						}
						return commands.Success(inv.CommandContext().CorrelationID()), nil
					}}, nil
				}))
				if !terminalFirst {
					addTerminal()
				}
				must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (int, error) {
					if fail == "nested" {
						_, _ = inv.Pipeline().Execute(ctx, Child{})
					}
					return 42, nil
				})))
				must(t, commands.Register[Child](&r, commands.Void(func(Child, context.Context) error { return cause })))
				p := build(t, &r, commands.PipelineOptions{})
				result, err := p.Execute(t.Context(), Parent{})
				if !reflect.DeepEqual(order, []string{"reserve", "begin", "ordinary", "terminal"}) {
					t.Fatal(order)
				}
				if fail == "" {
					must(t, err)
					if commits != 1 || !result.IsSuccess() || result.Completion().Disposition != commands.Committed {
						t.Fatal(result, commits)
					}
				} else {
					if commits != 0 || result.IsSuccess() || !errors.Is(err, cause) || result.Completion().Disposition != commands.NotCommitted {
						t.Fatal(result, commits, err)
					}
				}
			})
		}
	}
}

func TestFailedTerminalRetractsEveryResponseAndPreservesDisposition(t *testing.T) {
	for _, value := range []any{"new-id", false, 0, "", Event{Name: "ordinary object"}} {
		for _, disposition := range []commands.CommitDisposition{commands.NotCommitted, commands.OutcomeUnknown, commands.Committed, commands.MixedCommit} {
			var r commands.Registry
			cause := errors.New("append or acknowledgement failure")
			must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (any, error) { return value, nil })))
			must(t, r.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
				return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(context.Context, *commands.Invocation, commands.Result[any]) (commands.CompletionReport, error) {
					return commands.CompletionReport{Disposition: disposition}, cause
				}}, nil
			}))
			result, err := build(t, &r, commands.PipelineOptions{}).Execute(t.Context(), Clear{})
			var completion *commands.CompletionError
			if result.IsSuccess() || !errors.Is(err, cause) || !errors.As(err, &completion) || completion.Report.Disposition != disposition || result.Completion().Disposition != disposition {
				t.Fatal(result, err)
			}
			if _, present := result.Response(); present {
				t.Fatal("response survived failure")
			}
			body, err := json.Marshal(result)
			must(t, err)
			if strings.Contains(strings.ToLower(string(body)), "completion") || strings.Contains(strings.ToLower(string(body)), "disposition") {
				t.Fatal(string(body))
			}
		}
	}
}

func TestTerminalDispositionDoesNotInferSuccessFromNilError(t *testing.T) {
	for _, disposition := range []commands.CommitDisposition{commands.NoPersistedWork, commands.NotCommitted, commands.Committed, commands.OutcomeUnknown, commands.MixedCommit} {
		var registry commands.Registry
		must(t, commands.Register[Clear](&registry, commands.Void(func(Clear, context.Context) error { return nil })))
		must(t, registry.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
			return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(context.Context, *commands.Invocation, commands.Result[any]) (commands.CompletionReport, error) {
				return commands.CompletionReport{Disposition: disposition}, nil
			}}, nil
		}))
		result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), Clear{})
		wantSuccess := disposition == commands.NoPersistedWork || disposition == commands.Committed
		if result.IsSuccess() != wantSuccess || result.Completion().Disposition != disposition || (err == nil) != wantSuccess {
			t.Fatal(result, err)
		}
	}
}

func TestTerminalPanicRetainsObservationOrReportsUnknown(t *testing.T) {
	for _, reportFirst := range []bool{false, true} {
		var registry commands.Registry
		must(t, commands.Register[Clear](&registry, commands.Void(func(Clear, context.Context) error { return nil })))
		must(t, registry.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
			return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(ctx context.Context, inv *commands.Invocation, _ commands.Result[any]) (commands.CompletionReport, error) {
				if reportFirst {
					must(t, commands.ReportCommit(ctx, inv, commands.CompletionReport{Disposition: commands.Committed}))
				}
				panic("after possible dispatch")
			}}, nil
		}))
		result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), Clear{})
		want := commands.OutcomeUnknown
		if reportFirst {
			want = commands.Committed
		}
		var completion *commands.CompletionError
		if result.IsSuccess() || !errors.As(err, &completion) || completion.Report.Disposition != want || result.Completion().Disposition != want {
			t.Fatal(result, err)
		}
		if converted := commands.FromError[commands.NoResponse](result.Details().CorrelationID, err); converted.Completion().Disposition != want {
			t.Fatal("error conversion lost report")
		}
	}
}

func TestCancellationDuringOrdinaryCompletionPreventsTerminalCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var registry commands.Registry
	must(t, commands.Register[Clear](&registry, commands.Void(func(Clear, context.Context) error { return nil })))
	must(t, registry.AddExecutionScope("cancel", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
		return participant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(_ context.Context, inv *commands.Invocation, _ commands.Result[any]) (commands.Result[commands.NoResponse], error) {
			cancel()
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}}, nil
	}))
	must(t, registry.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
		return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(ctx context.Context, _ *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
			if result.IsSuccess() || ctx.Err() != nil {
				t.Fatal("terminal did not receive failed outcome and live cleanup")
			}
			return commands.CompletionReport{Disposition: commands.NotCommitted}, nil
		}}, nil
	}))
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(ctx, Clear{})
	if result.IsSuccess() || !errors.Is(err, context.Canceled) || result.Completion().Disposition != commands.NotCommitted {
		t.Fatal(result, err)
	}
}

func TestEarlyCommitAndCleanupFailureRetainEvidence(t *testing.T) {
	for _, early := range []bool{true, false} {
		var r commands.Registry
		cause := errors.New("post-commit failure")
		must(t, commands.Register[Clear](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Clear) (int, error) {
			if early {
				must(t, commands.ReportCommit(ctx, inv, commands.CompletionReport{Disposition: commands.Committed}))
				return 0, cause
			}
			return 1, nil
		})))
		must(t, r.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
			return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(_ context.Context, _ *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
				if early {
					if result.Completion().Disposition != commands.Committed {
						t.Fatal("early report lost")
					}
					return commands.CompletionReport{}, nil
				}
				return commands.CompletionReport{Disposition: commands.Committed}, nil
			}}, nil
		}))
		p := build(t, &r, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return closeFailure{cause}, nil }})
		result, err := commands.Execute[int](t.Context(), p, Clear{})
		var completion *commands.CompletionError
		if !errors.Is(err, cause) || !errors.As(err, &completion) || result.Completion().Disposition != commands.Committed || completion.Report.Disposition != commands.Committed {
			t.Fatal(result, err)
		}
		if _, present := result.Response(); present {
			t.Fatal("cleanup failure kept response")
		}
	}
}

type closeFailure struct{ err error }

func (c closeFailure) Close(context.Context) error { return c.err }

func TestSerializationErrorPreservesCommitReport(t *testing.T) {
	cause := errors.New("encode failure")
	r := commands.NewResult(commands.Details{Authorized: true, Completion: commands.CompletionReport{Disposition: commands.Committed}}, serialization.Some(1))
	_, err := r.MarshalJSONWith(func(any) ([]byte, error) { return nil, cause })
	var completion *commands.CompletionError
	if !errors.Is(err, cause) || !errors.As(err, &completion) || completion.Report.Disposition != commands.Committed {
		t.Fatal(err)
	}
}

func TestTerminalPartialBeginValidationSuppressionAndDuplicateRejection(t *testing.T) {
	var r commands.Registry
	begins, completes := 0, 0
	cause := errors.New("reserve failure")
	factory := func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
		return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { begins++; return cause }, complete: func(_ context.Context, _ *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
			completes++
			if result.IsSuccess() {
				t.Fatal("failed begin ignored")
			}
			return commands.CompletionReport{Disposition: commands.NotCommitted}, nil
		}}, nil
	}
	must(t, r.AddDeferredCommitParticipant("one", factory))
	if err := r.AddDeferredCommitParticipant("two", factory); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
	must(t, commands.Register[Clear](&r, commands.Void(func(Clear, context.Context) error { t.Fatal("Handle ran"); return nil })))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Validate(t.Context(), Clear{})
	must(t, err)
	if !result.IsSuccess() || begins != 0 || completes != 0 {
		t.Fatal(result, begins, completes)
	}
	_, err = p.Execute(t.Context(), Clear{})
	if !errors.Is(err, cause) || begins != 1 || completes != 1 {
		t.Fatal(err, begins, completes)
	}
}

func TestTerminalValidationCannotBeSuppressedAndCannotStartNestedWork(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (int, error) { return 42, nil })))
	must(t, r.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
		return terminalParticipant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(ctx context.Context, inv *commands.Invocation, _ commands.Result[any]) (commands.CompletionReport, error) {
			_, err := inv.Pipeline().Execute(ctx, Clear{})
			if !errors.Is(err, commands.ErrExecutionMismatch) {
				t.Fatal(err)
			}
			return commands.CompletionReport{Disposition: commands.NotCommitted}, validation.Reject(validation.Result{Severity: validation.Warning, Reason: validation.ConstraintViolation, Message: "store rejected"})
		}}, nil
	}))
	warning := validation.Warning
	result, err := build(t, &r, commands.PipelineOptions{}).Execute(t.Context(), Clear{}, commands.ExecuteOptions{AllowedSeverity: &warning})
	if result.IsSuccess() || result.IsValid() || !errors.Is(err, validation.ErrRejected) {
		t.Fatal(result, err)
	}
}
