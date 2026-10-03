// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
)

type participant struct {
	begin    func(context.Context, *commands.Invocation) error
	complete func(context.Context, *commands.Invocation, commands.Result[any]) (commands.Result[commands.NoResponse], error)
}

func (p participant) Begin(ctx context.Context, inv *commands.Invocation) error {
	return p.begin(ctx, inv)
}
func (p participant) Complete(ctx context.Context, inv *commands.Invocation, r commands.Result[any]) (commands.Result[commands.NoResponse], error) {
	return p.complete(ctx, inv, r)
}
func TestPartialBeginAndReverseCompletionDespiteErrors(t *testing.T) {
	for _, failBegin := range []bool{false, true} {
		t.Run(map[bool]string{false: "completion failure", true: "begin failure"}[failBegin], func(t *testing.T) {
			var r commands.Registry
			order := []string{}
			cause := errors.New("participant failure")
			must(t, commands.Register(&r, commands.Handle(Rename.Handle)))
			for _, name := range []string{"one", "two", "three"} {
				must(t, r.AddExecutionScope(name, func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
					return participant{begin: func(context.Context, *commands.Invocation) error {
						order = append(order, "begin-"+name)
						if failBegin && name == "two" {
							return cause
						}
						return nil
					}, complete: func(_ context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.Result[commands.NoResponse], error) {
						order = append(order, "complete-"+name)
						if !inv.Execution().IsRoot() {
							t.Fatal("participant not root")
						}
						if name == "two" {
							return commands.Success(inv.CommandContext().CorrelationID()), cause
						}
						return commands.Success(inv.CommandContext().CorrelationID()), nil
					}}, nil
				}))
			}
			p := build(t, &r, commands.PipelineOptions{})
			result, err := p.Execute(t.Context(), Rename{"response"})
			if !errors.Is(err, cause) || result.IsSuccess() {
				t.Fatal(result.Details(), err)
			}
			if _, present := result.Response(); present {
				t.Fatal("completion failure published response")
			}
			want := []string{"begin-one", "begin-two", "begin-three", "complete-three", "complete-two", "complete-one"}
			if failBegin {
				want = []string{"begin-one", "begin-two", "complete-two", "complete-one"}
			}
			if !reflect.DeepEqual(order, want) {
				t.Fatal(order, want)
			}
		})
	}
}

type Parent struct{}
type Child struct{}

func TestNestedExecutionSharesOwnerResourcesAndDistinctReceipts(t *testing.T) {
	var r commands.Registry
	opens, begins, completes := 0, 0, 0
	clockCalls := 0
	var parentReceipt time.Time
	var parentID correlation.ID
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (int, error) {
		if !inv.Execution().IsRoot() {
			t.Fatal("parent not root")
		}
		parentReceipt, parentID = inv.CommandContext().ReceivedAt(), inv.CommandContext().CorrelationID()
		result, err := inv.Pipeline().Execute(ctx, Child{})
		if err != nil || !result.IsSuccess() {
			t.Fatal(result.Details(), err)
		}
		return 42, nil
	})))
	must(t, commands.Register[Child](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
		if inv.Execution().IsRoot() || inv.CommandContext().CorrelationID() != parentID || inv.CommandContext().ReceivedAt().Equal(parentReceipt) {
			t.Fatal("nested metadata/owner not distinct")
		}
		_, err := execution.ResourcesAs[*resource](ctx, inv.Scope())
		return commands.NoResponse{}, err
	})))
	must(t, r.AddExecutionScope("root", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
		return participant{begin: func(context.Context, *commands.Invocation) error { begins++; return nil }, complete: func(_ context.Context, inv *commands.Invocation, _ commands.Result[any]) (commands.Result[commands.NoResponse], error) {
			completes++
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}}, nil
	}))
	p := build(t, &r, commands.PipelineOptions{Clock: func() time.Time { clockCalls++; return time.Unix(int64(clockCalls), 0) }, OpenResources: func(context.Context) (execution.Resources, error) { opens++; return &resource{}, nil }})
	result, err := p.Execute(t.Context(), Parent{})
	must(t, err)
	if !result.IsSuccess() || opens != 1 || begins != 1 || completes != 1 {
		t.Fatal(result.Details(), opens, begins, completes)
	}
}
func TestNestedValidateFailureIsAdvisoryDuringExecute(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (int, error) {
		child, err := inv.Pipeline().Validate(ctx, Child{})
		must(t, err)
		if child.IsSuccess() {
			t.Fatal("child validation unexpectedly passed")
		}
		return 42, nil
	})))
	must(t, commands.Register[Child](&r, commands.Void(func(Child, context.Context) error {
		t.Fatal("Validate invoked child Handle")
		return nil
	}), commands.WithAuthorization[Child](metadata.Authorization{})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Parent{})
	must(t, err)
	if value, present := result.Response(); !result.IsSuccess() || !present || value != 42 {
		t.Fatal("advisory validation contaminated parent execution", result.Details(), value)
	}
}

func TestNestedValidateFailureRemainsStickyDuringValidate(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Parent](&r, commands.Void(func(Parent, context.Context) error { return nil })))
	must(t, commands.Register[Child](&r, commands.Void(func(Child, context.Context) error { return nil }), commands.WithAuthorization[Child](metadata.Authorization{})))
	must(t, r.AddFilter("nested-validation", func(context.Context, *execution.Scope) (commands.Filter, error) {
		return commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			if _, parent := inv.CommandContext().Command().(Parent); parent {
				_, _ = inv.Pipeline().Validate(ctx, Child{})
			}
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}), nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Validate(t.Context(), Parent{})
	must(t, err)
	if result.IsSuccess() {
		t.Fatal("validation-only parent lost nested validation failure")
	}
}

func TestEarlyCompletionGuardObservesIgnoredNestedFailure(t *testing.T) {
	for _, advisory := range []bool{false, true} {
		var registry commands.Registry
		var retained *commands.Execution
		var retainedContext context.Context
		must(t, commands.Register[Parent](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
			retained, retainedContext = inv.Execution(), ctx
			must(t, retained.CheckRecordedFailures(ctx))
			if advisory {
				_, _ = inv.Pipeline().Validate(ctx, Child{})
			} else {
				_, _ = inv.Pipeline().Execute(ctx, Child{})
			}
			err := retained.CheckRecordedFailures(ctx)
			if advisory {
				must(t, err)
			} else if !errors.Is(err, commands.ErrExecutionFailed) {
				t.Fatal(err)
			}
			return commands.NoResponse{}, nil
		})))
		must(t, commands.Register[Child](&registry, commands.Void(func(Child, context.Context) error { t.Fatal("denied child executed"); return nil }), commands.WithAuthorization[Child](metadata.Authorization{})))
		result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), Parent{})
		must(t, err)
		if result.IsSuccess() != advisory {
			t.Fatal(result.Details())
		}
		if err := retained.CheckRecordedFailures(retainedContext); !errors.Is(err, commands.ErrExecutionClosed) {
			t.Fatal(err)
		}
	}
}

func TestEarlyCompletionGuardChecksAncestorFailures(t *testing.T) {
	var registry commands.Registry
	must(t, commands.Register[Parent](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		_, _ = inv.Pipeline().Execute(ctx, Grandchild{})
		_, _ = inv.Pipeline().Execute(ctx, Child{})
		return commands.NoResponse{}, nil
	})))
	must(t, commands.Register[Grandchild](&registry, commands.Void(func(Grandchild, context.Context) error { return nil }), commands.WithAuthorization[Grandchild](metadata.Authorization{})))
	must(t, commands.Register[Child](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
		if err := inv.Execution().CheckRecordedFailures(ctx); !errors.Is(err, commands.ErrExecutionFailed) {
			t.Fatal(err)
		}
		return commands.NoResponse{}, nil
	})))
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), Parent{})
	must(t, err)
	if result.IsSuccess() {
		t.Fatal(result.Details())
	}
}

func TestIgnoredNestedFailureIsSticky(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (int, error) {
		_, _ = inv.Pipeline().Execute(ctx, Child{})
		return 42, nil
	})))
	must(t, commands.Register[Child](&r, commands.Void(func(Child, context.Context) error { t.Fatal("denied child handled"); return nil }), commands.WithAuthorization[Child](metadata.Authorization{})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Parent{})
	must(t, err)
	if result.IsAuthorized() {
		t.Fatal("discarded child failure became success")
	}
	if _, present := result.Response(); present {
		t.Fatal("sticky failure retained response")
	}
}

type Grandchild struct{}

func TestNestedIntermediateResultRetainsIgnoredGrandchildFailure(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (int, error) {
		child, err := inv.Pipeline().Execute(ctx, Child{})
		if err != nil {
			return 0, err
		}
		if child.IsAuthorized() || child.IsSuccess() {
			t.Error("intermediate child reported success after ignored grandchild denial")
		}
		return 42, nil
	})))
	must(t, commands.Register[Child](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Child) (int, error) {
		_, _ = inv.Pipeline().Execute(ctx, Grandchild{})
		return 1, nil
	})))
	must(t, commands.Register[Grandchild](&r, commands.Void(func(Grandchild, context.Context) error { t.Fatal("denied grandchild handled"); return nil }), commands.WithAuthorization[Grandchild](metadata.Authorization{})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Parent{})
	must(t, err)
	if result.IsAuthorized() || result.IsSuccess() {
		t.Fatal("root lost grandchild failure", result.Details())
	}
	if len(result.Details().ExceptionMessages) != 0 {
		t.Fatal(result.Details())
	}
}

func TestRootExecutorInsideHandlerOpensIndependentOwner(t *testing.T) {
	var r commands.Registry
	opens := 0
	var p commands.Pipeline
	must(t, commands.Register[Parent](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		result, err := p.Execute(ctx, Child{})
		if !result.IsSuccess() {
			t.Fatal(result.Details())
		}
		return commands.NoResponse{}, err
	})))
	must(t, commands.Register[Child](&r, commands.Invoke(func(_ context.Context, inv *commands.Invocation, _ Child) (commands.NoResponse, error) {
		if !inv.Execution().IsRoot() {
			t.Fatal("unbound root joined owner")
		}
		return commands.NoResponse{}, nil
	})))
	p = build(t, &r, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { opens++; return &resource{}, nil }})
	_, err := p.Execute(t.Context(), Parent{})
	must(t, err)
	if opens != 2 {
		t.Fatal(opens)
	}
}
func TestExpiredSecurityMismatchAndValidationOnlyExecutors(t *testing.T) {
	var r commands.Registry
	var retained commands.Pipeline
	must(t, commands.Register[Parent](&r, commands.Invoke(func(_ context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		retained = inv.Pipeline()
		return commands.NoResponse{}, nil
	})))
	must(t, commands.Register[Child](&r, commands.Void(func(Child, context.Context) error { return nil })))
	p := build(t, &r, commands.PipelineOptions{})
	_, err := p.Execute(t.Context(), Parent{})
	must(t, err)
	if _, err := retained.Execute(t.Context(), Child{}); !errors.Is(err, commands.ErrExecutionClosed) {
		t.Fatal(err)
	}
	var guarded commands.Registry
	must(t, commands.Register[Parent](&guarded, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Parent) (commands.NoResponse, error) {
		changed := identity.WithPrincipal(ctx, identity.System())
		_, err := inv.Pipeline().Execute(changed, Child{})
		if !errors.Is(err, execution.ErrIdentityChanged) {
			t.Fatal(err)
		}
		return commands.NoResponse{}, nil
	})))
	must(t, commands.Register[Child](&guarded, commands.Void(func(Child, context.Context) error { t.Fatal("mismatched child invoked"); return nil })))
	p = build(t, &guarded, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Parent{})
	if !errors.Is(err, execution.ErrIdentityChanged) || result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
	var validationOnly commands.Registry
	must(t, commands.Register[Parent](&validationOnly, commands.Void(func(Parent, context.Context) error { t.Fatal("validation handled"); return nil })))
	must(t, validationOnly.AddFilter("invalid nested execute", func(context.Context, *execution.Scope) (commands.Filter, error) {
		return commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			_, err := inv.Pipeline().Execute(ctx, Parent{})
			if !errors.Is(err, commands.ErrExecutionMismatch) {
				t.Fatal(err)
			}
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}), nil
	}))
	p = build(t, &validationOnly, commands.PipelineOptions{})
	resultValidation, err := p.Validate(t.Context(), Parent{})
	if !errors.Is(err, commands.ErrExecutionMismatch) || resultValidation.IsSuccess() {
		t.Fatal("Validate-only executor did not retain failure", err)
	}
}
func TestCancellationCompletionHasLiveContextAndFailedOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (int, error) { cancel(); return 42, nil })))
	completed := false
	must(t, r.AddExecutionScope("observe", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
		return participant{begin: func(context.Context, *commands.Invocation) error { return nil }, complete: func(ctx context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.Result[commands.NoResponse], error) {
			completed = true
			if ctx.Err() != nil || result.IsSuccess() {
				t.Fatal("completion saw live-success after cancellation")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded cleanup")
			}
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}}, nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(ctx, Clear{})
	if !errors.Is(err, context.Canceled) || result.IsSuccess() || !completed {
		t.Fatal(result.Details(), err, completed)
	}
}
