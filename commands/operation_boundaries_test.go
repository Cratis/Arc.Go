// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
)

func TestOperationTypedNestedPrecheckCannotBypassStickyOriginalBoundary(t *testing.T) {
	probe := &operationProbe{}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	var pipeline commands.Pipeline
	childCalls := 0
	must(t, commands.Register[ordinaryOperationChild](&registry, commands.Handle(func(ordinaryOperationChild, context.Context) (string, error) { childCalls++; return "response", nil })))
	must(t, commands.Register[operationOutcomeCommand](&registry, commands.Invoke(func(ctx context.Context, _ *commands.Invocation, _ operationOutcomeCommand) (commands.Operations, error) {
		// Even mismatched typed-response prechecking must first refuse nesting.
		_, _ = commands.Execute[int](ctx, pipeline, ordinaryOperationChild{})
		return operationBatch(t, namedOperation{"A"}), nil
	})))
	pipeline = build(t, &registry, commands.PipelineOptions{})
	result, err := pipeline.Execute(t.Context(), operationOutcomeCommand{})
	if result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) || childCalls != 0 || len(probe.calls) != 0 {
		t.Fatal(result.Details(), err, childCalls, probe.calls)
	}
}

func TestOperationRecoveryNestingUsesPerCallbackAttemptsAndContinues(t *testing.T) {
	original := errors.New("original:B")
	probe := &operationProbe{forward: func(_ context.Context, name string) error {
		if name == "B" {
			return original
		}
		return nil
	}}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	childCalls := 0
	must(t, commands.Register[ordinaryOperationChild](&registry, commands.Void(func(ordinaryOperationChild, context.Context) error { childCalls++; return nil })))
	pipeline := build(t, &registry, commands.PipelineOptions{})
	probe.reverse = func(ctx context.Context, name string, _ commands.OperationFailure) error {
		if name == "B" {
			_, _ = pipeline.Validate(ctx, ordinaryOperationChild{})
		}
		return nil
	}
	result, err := pipeline.Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"}, namedOperation{"B"})})
	if !errors.Is(err, original) || errors.Is(err, commands.ErrInvalidOperation) || childCalls != 0 {
		t.Fatal(err, childCalls)
	}
	assertOperationCalls(t, probe, "execute:A", "execute:B", "compensate:B", "compensate:A")
	operationSummary(t, result, commands.RecoveryIncomplete, 2, 1, 1)
	outcomes := result.OperationOutcomes()
	if outcomes[0].Compensation != commands.CompensationCompleted || !errors.Is(outcomes[1].CompensationFailure, commands.ErrInvalidOperation) {
		t.Fatal(outcomes)
	}
}

type operationSecurityResources struct {
	*operationProbe
	rejectForward bool
}

func (r *operationSecurityResources) CheckContext(ctx context.Context) error {
	if _, cleanup := ctx.Deadline(); r.rejectForward && !cleanup {
		return execution.ErrIdentityChanged
	}
	return nil
}

func TestOperationSecurityRefusalAfterPreflightCreatesNoJournalEntry(t *testing.T) {
	resources := &operationSecurityResources{operationProbe: &operationProbe{}}
	var registry commands.Registry
	must(t, commands.RegisterOperation[namedOperation](&registry, func(context.Context, *execution.Scope) (*operationProbe, error) {
		resources.rejectForward = true
		return resources.operationProbe, nil
	}))
	must(t, commands.Register[operationBatchCommand](&registry, commands.Handle(operationBatchCommand.Handle)))
	pipeline := build(t, &registry, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
	result, err := pipeline.Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
	if !errors.Is(err, execution.ErrIdentityChanged) || result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
	assertOperationCalls(t, resources.operationProbe, "dispose")
	operationSummary(t, result, commands.RecoveryNotNeeded, 0, 0, 0)
}

func TestOperationOrdinaryCompletionFailurePrecedesTerminalAndRecovery(t *testing.T) {
	original := errors.New("ordinary scope failed")
	probe := &operationProbe{}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	scope := &operationScope{probe: probe, name: "ordinary", complete: func(commands.Result[any]) (commands.Result[commands.NoResponse], error) {
		return commands.Result[commands.NoResponse]{}, original
	}}
	must(t, registry.AddOperationExecutionScope("ordinary", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) { return scope, nil }))
	terminal := &operationTerminal{probe: probe, complete: func(_ context.Context, _ *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
		if result.IsSuccess() {
			t.Fatal("terminal did not see failed ordinary completion")
		}
		return commands.CompletionReport{Disposition: commands.NotCommitted}, nil
	}}
	addOperationTerminal(t, &registry, terminal)
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
	if !errors.Is(err, original) {
		t.Fatal(err)
	}
	assertOperationCalls(t, probe, "begin:terminal", "begin:ordinary", "execute:A", "complete:ordinary", "complete:terminal", "compensate:A")
	operationSummary(t, result, commands.RecoveryCompleted, 1, 1, 1)
	if failure := probe.failures[0]; failure.Source != commands.FailureScopeCompletion || !errors.Is(failure.Cause, original) {
		t.Fatal(failure)
	}
}

func TestOperationCommitObservationFailureIsUnknownAndDoesNotEnter(t *testing.T) {
	probe := &operationProbe{}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	lost := errors.New("observation unavailable")
	terminal := &operationTerminal{probe: probe, observeErr: lost}
	addOperationTerminal(t, &registry, terminal)
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
	if !errors.Is(err, lost) || result.Completion().Disposition != commands.OutcomeUnknown {
		t.Fatal(err, result.Completion())
	}
	assertOperationCalls(t, probe, "begin:terminal", "complete:terminal")
	operationSummary(t, result, commands.RecoveryNotNeeded, 0, 0, 0)
}

type pendingOperationResources struct {
	*operationProbe
	pending       bool
	closes, joins int
}

func (r *pendingOperationResources) Close(ctx context.Context) error {
	r.closes++
	return r.operationProbe.Close(ctx)
}
func (r *pendingOperationResources) Join(ctx context.Context) error {
	r.joins++
	if r.pending {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func TestOperationPendingDisposalCanJoinButNeverReexecuteOrRecompensate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		original := errors.New("original")
		probe := &operationProbe{forward: func(context.Context, string) error { return original }}
		resources := &pendingOperationResources{operationProbe: probe, pending: true}
		var registry commands.Registry
		registerOperations(t, &registry, probe)
		pipeline := build(t, &registry, commands.PipelineOptions{CleanupTimeout: time.Second, OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
		result, err := pipeline.Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
		var pending *execution.PendingScopeError
		if !errors.Is(err, original) || !errors.As(err, &pending) {
			t.Fatal(err)
		}
		operationSummary(t, result, commands.RecoveryCompleted, 1, 0, 1)
		resources.pending = false
		must(t, pending.Scope().Close(t.Context()))
		must(t, pending.Scope().Close(t.Context()))
		assertOperationCalls(t, probe, "execute:A", "compensate:A", "dispose")
		if resources.closes != 1 || resources.joins != 2 {
			t.Fatal(resources.closes, resources.joins)
		}
	})
}

func TestOperationPanicIsTheFailingInvocation(t *testing.T) {
	probe := &operationProbe{forward: func(context.Context, string) error { panic("original") }}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	_, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
	var panicErr *execution.PanicError
	if !errors.As(err, &panicErr) || !probe.failures[0].IsFailingInvocation {
		t.Fatal(err, probe.failures)
	}
}

func TestOperationOptionsRejectNegativeBudgetsWithoutActivation(t *testing.T) {
	var registry commands.Registry
	if _, err := registry.Build(commands.PipelineOptions{Operations: commands.OperationOptions{CompensationTimeout: -time.Second}}); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
}
