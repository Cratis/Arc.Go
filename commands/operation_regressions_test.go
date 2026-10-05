// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
)

func TestOperationChildRefusalImmediatelyGuardsOrdinaryParentWrites(t *testing.T) {
	for _, originalPipeline := range []bool{false, true} {
		name := "bound"
		if originalPipeline {
			name = "original"
		}
		t.Run(name, func(t *testing.T) {
			probe := &operationProbe{}
			var registry commands.Registry
			registerOperations(t, &registry, probe)
			var pipeline commands.Pipeline
			writes := 0
			must(t, commands.Register[ordinaryOperationChild](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ ordinaryOperationChild) (commands.NoResponse, error) {
				executor := inv.Pipeline()
				if originalPipeline {
					executor = pipeline
				}
				// Deliberately discard the child failure before an integration-owned
				// explicit persistence guard, still inside this same callback.
				_, _ = executor.Execute(ctx, operationBatchCommand{operationBatch(t, namedOperation{"A"})})
				guardErr := inv.Execution().CheckRecordedFailures(ctx)
				if !errors.Is(guardErr, commands.ErrExecutionFailed) || !errors.Is(guardErr, commands.ErrInvalidOperation) {
					t.Errorf("immediate recorded failure guard = %v", guardErr)
				}
				if guardErr == nil && inv.Execution().CheckExplicitCommit(ctx) == nil {
					writes++
				}
				return commands.NoResponse{}, nil
			})))
			pipeline = build(t, &registry, commands.PipelineOptions{})
			result, err := pipeline.Execute(t.Context(), ordinaryOperationChild{})
			if result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) || writes != 0 || len(probe.calls) != 0 {
				t.Fatal(result.Details(), err, writes, probe.calls)
			}
		})
	}
}

type outcomeEmbeddingOperation[R any] struct {
	commands.Outcome[R]
	executions *int
}

func (outcomeEmbeddingOperation[R]) CommandOperation() {}
func (o outcomeEmbeddingOperation[R]) Execute(context.Context, struct{}) error {
	(*o.executions)++
	return nil
}

func TestOperationOutcomeEmbeddingRemainsAReservedServerLeaf(t *testing.T) {
	t.Run("empty void outcome", func(t *testing.T) {
		testOperationOutcomeEmbedding(t, commands.Outcome[commands.NoResponse]{})
	})
	t.Run("empty response outcome", func(t *testing.T) {
		testOperationOutcomeEmbedding(t, commands.Outcome[int]{})
	})
	t.Run("populated response outcome", func(t *testing.T) {
		testOperationOutcomeEmbedding(t, commands.Respond(42))
	})
}

func testOperationOutcomeEmbedding[R any](t *testing.T, embedded commands.Outcome[R]) {
	t.Helper()
	for _, shape := range []string{"singular", "batch", "erased", "effect", "response"} {
		t.Run(shape, func(t *testing.T) {
			var registry commands.Registry
			factories, executions := 0, 0
			operation := outcomeEmbeddingOperation[R]{Outcome: embedded, executions: &executions}
			must(t, commands.RegisterOperation[outcomeEmbeddingOperation[R]](&registry, func(context.Context, *execution.Scope) (struct{}, error) {
				factories++
				return struct{}{}, nil
			}))
			switch shape {
			case "singular":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (outcomeEmbeddingOperation[R], error) {
					return operation, nil
				})))
			case "batch":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Operations, error) {
					return operationBatch(t, operation), nil
				})))
			case "erased":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (any, error) { return operation, nil })))
			case "effect":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Outcome[commands.NoResponse], error) {
					return commands.Effects[commands.NoResponse](operation), nil
				}), commands.WithOperations[operationOutcomeCommand]()))
			case "response":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Outcome[any], error) {
					return commands.Respond[any](operation), nil
				}), commands.WithOperations[operationOutcomeCommand]()))
			}
			pipeline := build(t, &registry, commands.PipelineOptions{})
			entry, lookupErr := pipeline.LookupCommand(operationOutcomeCommand{})
			must(t, lookupErr)
			if shape == "singular" || shape == "batch" || shape == "effect" {
				if _, present := entry.ResponseType(); present || entry.ResponseKind() != commands.ResponseNone {
					t.Fatal("server-only operation exposed response metadata", entry.ResponseKind())
				}
			}
			result, err := pipeline.Execute(t.Context(), operationOutcomeCommand{})
			if shape == "erased" || shape == "response" {
				if result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) || factories != 0 || executions != 0 {
					t.Fatal(result.Details(), err, factories, executions)
				}
			} else if !result.IsSuccess() || err != nil || factories != 1 || executions != 1 {
				t.Fatal(result.Details(), err, factories, executions)
			}
			if _, present := result.Response(); present {
				t.Fatal("operation embedding produced a client response")
			}
		})
	}
}
