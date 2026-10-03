// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
)

type operationProbe struct {
	calls    []string
	failures []commands.OperationFailure
	forward  func(context.Context, string) error
	reverse  func(context.Context, string, commands.OperationFailure) error
	closed   bool
	closeErr error
}

func (p *operationProbe) Close(context.Context) error {
	p.closed = true
	p.calls = append(p.calls, "dispose")
	return p.closeErr
}

type namedOperation struct{ Name string }

func (namedOperation) CommandOperation() {}
func (o namedOperation) Execute(ctx context.Context, p *operationProbe) error {
	if p.closed {
		return errors.New("dependency disposed before execution")
	}
	p.calls = append(p.calls, "execute:"+o.Name)
	if p.forward != nil {
		return p.forward(ctx, o.Name)
	}
	return nil
}
func (o namedOperation) Compensate(ctx context.Context, p *operationProbe, failure commands.OperationFailure) error {
	if p.closed {
		return errors.New("dependency disposed before recovery")
	}
	p.calls = append(p.calls, "compensate:"+o.Name)
	p.failures = append(p.failures, failure)
	if p.reverse != nil {
		return p.reverse(ctx, o.Name, failure)
	}
	return nil
}

type irreversibleOperation struct{ Name string }

func (irreversibleOperation) CommandOperation() {}
func (o irreversibleOperation) Execute(ctx context.Context, p *operationProbe) error {
	return namedOperation(o).Execute(ctx, p)
}

type recoveryDependencyBundle struct{ Forward, Recovery *operationProbe }
type missingRecoveryOperation struct{ Name string }

func (missingRecoveryOperation) CommandOperation() {}
func (o missingRecoveryOperation) Execute(ctx context.Context, d recoveryDependencyBundle) error {
	return namedOperation(o).Execute(ctx, d.Forward)
}
func (o missingRecoveryOperation) Compensate(ctx context.Context, d recoveryDependencyBundle, failure commands.OperationFailure) error {
	return namedOperation(o).Compensate(ctx, d.Recovery, failure)
}

type operationBatchCommand struct{ Batch commands.Operations }

func (c operationBatchCommand) Handle(context.Context) (commands.Operations, error) {
	return c.Batch, nil
}

type operationOutcomeCommand struct{}
type ordinaryOperationChild struct{}

func operationBatch(t *testing.T, values ...commands.Operation) commands.Operations {
	t.Helper()
	batch, err := commands.NewOperations(values...)
	must(t, err)
	return batch
}
func registerOperations(t *testing.T, r *commands.Registry, p *operationProbe) {
	t.Helper()
	factory := func(context.Context, *execution.Scope) (*operationProbe, error) { return p, nil }
	must(t, commands.RegisterOperation[namedOperation](r, factory))
	must(t, commands.RegisterOperation[irreversibleOperation](r, factory))
	must(t, commands.Register[operationBatchCommand](r, commands.Handle(operationBatchCommand.Handle)))
}
func assertOperationCalls(t *testing.T, p *operationProbe, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(p.calls, want) {
		t.Fatalf("calls = %v, want %v", p.calls, want)
	}
}
func operationSummary(t *testing.T, result commands.Result[any], status commands.RecoveryStatus, started, completed, compensated int) {
	t.Helper()
	summary, present := result.Recovery()
	if !present || summary.Status != status || summary.StartedCount != started || summary.CompletedCount != completed || summary.CompensatedCount != compensated {
		t.Fatalf("summary = %+v, present %v", summary, present)
	}
}

type operationScope struct {
	probe    *operationProbe
	name     string
	complete func(commands.Result[any]) (commands.Result[commands.NoResponse], error)
}

func (s *operationScope) Begin(context.Context, *commands.Invocation) error {
	s.probe.calls = append(s.probe.calls, "begin:"+s.name)
	return nil
}
func (s *operationScope) Complete(_ context.Context, _ *commands.Invocation, result commands.Result[any]) (commands.Result[commands.NoResponse], error) {
	s.probe.calls = append(s.probe.calls, "complete:"+s.name)
	if s.complete != nil {
		return s.complete(result)
	}
	return commands.Success(result.Details().CorrelationID), nil
}

type operationTerminal struct {
	probe       *operationProbe
	before      commands.CompletionReport
	observeErr  error
	report      commands.CompletionReport
	completeErr error
	complete    func(context.Context, *commands.Invocation, commands.Result[any]) (commands.CompletionReport, error)
}

func (s *operationTerminal) Begin(context.Context, *commands.Invocation) error {
	s.probe.calls = append(s.probe.calls, "begin:terminal")
	return nil
}
func (s *operationTerminal) ObserveCommit(context.Context, *commands.Invocation) (commands.CompletionReport, error) {
	return s.before, s.observeErr
}
func (s *operationTerminal) Complete(ctx context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
	s.probe.calls = append(s.probe.calls, "complete:terminal")
	if s.complete != nil {
		return s.complete(ctx, inv, result)
	}
	return s.report, s.completeErr
}
func addOperationTerminal(t *testing.T, r *commands.Registry, terminal *operationTerminal) {
	t.Helper()
	must(t, r.AddOperationCommitParticipant("terminal", func(context.Context, *execution.Scope) (commands.OperationCommitParticipant, error) {
		return terminal, nil
	}))
}

func TestOperationsExecuteSequentiallyAndRecoverPartialFailingInvocation(t *testing.T) {
	original := errors.New("original:C")
	p := &operationProbe{forward: func(_ context.Context, name string) error {
		if name == "C" {
			return original
		}
		return nil
	}}
	var registry commands.Registry
	registerOperations(t, &registry, p)
	pipeline := build(t, &registry, commands.PipelineOptions{})
	result, err := pipeline.Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"}, namedOperation{"B"}, namedOperation{"C"}, namedOperation{"D"})})
	if !errors.Is(err, original) || result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
	assertOperationCalls(t, p, "execute:A", "execute:B", "execute:C", "compensate:C", "compensate:B", "compensate:A")
	operationSummary(t, result, commands.RecoveryCompleted, 3, 2, 3)
	if _, present := result.Response(); present {
		t.Fatal("operation response leaked")
	}
	outcomes := result.OperationOutcomes()
	if len(outcomes) != 3 || outcomes[2].ExecutionCompleted || outcomes[2].OperationType != reflect.TypeFor[namedOperation]() {
		t.Fatal(outcomes)
	}
	outcomes[0].ExecutionCompleted = false
	if !result.OperationOutcomes()[0].ExecutionCompleted {
		t.Fatal("observation membership exposed")
	}
	for index, failure := range p.failures {
		if failure.InvocationIndex != 2-index || failure.IsFailingInvocation != (index == 0) || failure.InvocationCompleted != (index != 0) || failure.Source != commands.FailureExecution || !errors.Is(failure.Cause, original) || failure.Original.IsSuccess() {
			t.Fatalf("failure = %+v", failure)
		}
	}
}

func TestOperationRecoveryUsesAllExistingDispositionsAfterTerminal(t *testing.T) {
	for _, disposition := range []commands.CommitDisposition{commands.NoPersistedWork, commands.NotCommitted, commands.Committed, commands.OutcomeUnknown, commands.MixedCommit} {
		t.Run(string(rune('A'+disposition)), func(t *testing.T) {
			original := errors.New("original")
			probe := &operationProbe{forward: func(context.Context, string) error { return original }}
			var registry commands.Registry
			registerOperations(t, &registry, probe)
			for _, name := range []string{"one", "two"} {
				scope := &operationScope{probe: probe, name: name, complete: func(result commands.Result[any]) (commands.Result[commands.NoResponse], error) {
					if result.IsSuccess() {
						t.Fatal("scope lost original failure")
					}
					return commands.Success(result.Details().CorrelationID), nil
				}}
				must(t, registry.AddOperationExecutionScope(name, func(context.Context, *execution.Scope) (commands.ExecutionScope, error) { return scope, nil }))
			}
			terminal := &operationTerminal{probe: probe, report: commands.CompletionReport{Disposition: disposition}}
			addOperationTerminal(t, &registry, terminal)
			pipeline := build(t, &registry, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return probe, nil }})
			result, err := pipeline.Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
			if !errors.Is(err, original) || result.Completion().Disposition != disposition {
				t.Fatal(err, result.Completion())
			}
			want := []string{"begin:terminal", "begin:one", "begin:two", "execute:A", "complete:two", "complete:one", "complete:terminal"}
			status, compensated := commands.RecoveryCompleted, 1
			if disposition == commands.NoPersistedWork || disposition == commands.NotCommitted {
				want = append(want, "compensate:A")
			} else {
				status, compensated = commands.RecoveryIndeterminate, 0
				if disposition == commands.Committed {
					status = commands.RecoverySuppressed
				}
				if result.OperationOutcomes()[0].Compensation != commands.CompensationSuppressed {
					t.Fatal(result.OperationOutcomes())
				}
			}
			want = append(want, "dispose")
			assertOperationCalls(t, probe, want...)
			operationSummary(t, result, status, 1, 0, compensated)
		})
	}
}

func TestOperationsRejectMissingCompensationBundleBeforeConsumers(t *testing.T) {
	missing := errors.New("missing compensation dependency")
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing dependency", true: "unregistered declaration"}[malformed], func(t *testing.T) {
			probe := &operationProbe{}
			var registry commands.Registry
			factories, consumers := 0, 0
			must(t, commands.RegisterOperation[namedOperation](&registry, func(context.Context, *execution.Scope) (*operationProbe, error) { factories++; return probe, nil }))
			must(t, commands.RegisterOperation[missingRecoveryOperation](&registry, func(context.Context, *execution.Scope) (recoveryDependencyBundle, error) {
				factories++
				// Resolving the reversal-only service fails: reject the entire bundle.
				return recoveryDependencyBundle{Forward: probe}, missing
			}))
			second := commands.Operation(missingRecoveryOperation{"B"})
			if malformed {
				second = declarationOperation{}
			}
			must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Outcome[int], error) {
				return commands.Respond(0, namedOperation{"A"}, second, "event"), nil
			}), commands.WithOperations[operationOutcomeCommand]()))
			must(t, registry.AddResponseValueHandler("consumer", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
				consumers++
				return &operationConsumer{}, nil
			}))
			pipeline := build(t, &registry, commands.PipelineOptions{})
			result, err := pipeline.Execute(t.Context(), operationOutcomeCommand{})
			if err == nil || result.IsSuccess() || len(probe.calls) != 0 || consumers != 0 {
				t.Fatal(result.Details(), err, probe.calls, consumers)
			}
			if malformed && factories != 0 {
				t.Fatal("dependencies resolved before whole declaration validation", factories)
			}
			if !malformed && !errors.Is(err, missing) {
				t.Fatal(err)
			}
			operationSummary(t, result, commands.RecoveryNotNeeded, 0, 0, 0)
		})
	}
}

type operationConsumer struct {
	seen         []any
	fragment     commands.Result[commands.NoResponse]
	fail         bool
	updaterCalls int
}

func (c *operationConsumer) CanHandle(_ commands.CommandContext, value any) bool {
	c.seen = append(c.seen, value)
	return true
}
func (c *operationConsumer) UpdateContext(context.Context, *commands.Invocation, any) error {
	c.updaterCalls++
	return nil
}
func (c *operationConsumer) Handle(_ context.Context, inv *commands.Invocation, value any) (commands.Result[commands.NoResponse], error) {
	c.seen = append(c.seen, value)
	if c.fail {
		return c.fragment, nil
	}
	return commands.Success(inv.CommandContext().CorrelationID()), nil
}

func TestOperationLeavesReservedAndControlSeverityBeforeConsumers(t *testing.T) {
	for _, blocking := range []bool{true, false} {
		t.Run(map[bool]string{true: "blocking", false: "allowed warning"}[blocking], func(t *testing.T) {
			probe, consumer := &operationProbe{}, &operationConsumer{}
			var registry commands.Registry
			registerOperations(t, &registry, probe)
			must(t, registry.AddResponseValueHandler("broad", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) { return consumer, nil }))
			must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Outcome[int], error) {
				return commands.Respond(0, namedOperation{"A"}, validation.Result{Severity: validation.Warning, Message: "finding"}, "event"), nil
			}), commands.WithOperations[operationOutcomeCommand]()))
			pipeline := build(t, &registry, commands.PipelineOptions{})
			severity := validation.Information
			if !blocking {
				severity = validation.Warning
			}
			options := []commands.ExecuteOptions{{AllowedSeverity: &severity}}
			result, err := commands.Execute[int](t.Context(), pipeline, operationOutcomeCommand{}, options...)
			must(t, err)
			if result.IsSuccess() == blocking {
				t.Fatal(result.Details())
			}
			if blocking {
				if len(consumer.seen) != 0 || consumer.updaterCalls != 0 || len(probe.calls) != 0 {
					t.Fatal("effects before blocking control", consumer, probe.calls)
				}
				if _, present := result.Response(); present {
					t.Fatal("failed zero response retained")
				}
			} else {
				assertOperationCalls(t, probe, "execute:A")
				if value, present := result.Response(); !present || value != 0 {
					t.Fatal(value, present)
				}
				for _, value := range consumer.seen {
					if _, isOperation := value.(commands.Operation); isOperation {
						t.Fatal("broad consumer saw operation")
					}
				}
				if _, present := result.Recovery(); !present {
					t.Fatal("typed execution lost recovery")
				}
			}
		})
	}
}

func TestOperationsConsumerFailedFragmentStopsExecution(t *testing.T) {
	probe := &operationProbe{}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	consumer := &operationConsumer{fail: true, fragment: commands.WithValidationResults(correlation.ID{}, validation.Result{Severity: validation.Error, Message: "event rejected"})}
	must(t, registry.AddResponseValueHandler("event", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) { return consumer, nil }))
	must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Outcome[int], error) {
		return commands.Respond(0, namedOperation{"A"}, "event", "later event"), nil
	}), commands.WithOperations[operationOutcomeCommand]()))
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationOutcomeCommand{})
	must(t, err)
	if result.IsSuccess() || len(probe.calls) != 0 {
		t.Fatal(result.Details(), probe.calls)
	}
}

func TestOperationsRejectExplicitOperationResponseAndAmbiguity(t *testing.T) {
	for _, kind := range []string{"operation response", "nested operation response", "two responses", "erased effect without opt in", "raw operation slice"} {
		t.Run(kind, func(t *testing.T) {
			probe := &operationProbe{}
			var registry commands.Registry
			registerOperations(t, &registry, probe)
			handler := commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Outcome[any], error) {
				switch kind {
				case "operation response":
					return commands.Respond[any](namedOperation{"response"}, namedOperation{"A"}), nil
				case "nested operation response":
					return commands.Respond[any](commands.Values[any](namedOperation{"response"}), namedOperation{"A"}), nil
				case "two responses":
					return commands.Values[any](0, "second", namedOperation{"A"}), nil
				case "raw operation slice":
					return commands.Effects[any]([]namedOperation{{"A"}}), nil
				default:
					return commands.Effects[any](namedOperation{"A"}), nil
				}
			})
			options := []commands.Option[operationOutcomeCommand]{handler}
			if kind != "erased effect without opt in" {
				options = append(options, commands.WithOperations[operationOutcomeCommand]())
			}
			must(t, commands.Register[operationOutcomeCommand](&registry, options...))
			result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationOutcomeCommand{})
			if err == nil || result.IsSuccess() || len(probe.calls) != 0 {
				t.Fatal(result.Details(), err, probe.calls)
			}
			if _, present := result.Response(); present {
				t.Fatal("ambiguous response published")
			}
		})
	}
}

func TestOperationsNilEmptyAndOrdinaryContainerBoundaries(t *testing.T) {
	for _, kind := range []string{"nil concrete", "nil interface", "empty batch", "single", "ordinary DTO collection"} {
		t.Run(kind, func(t *testing.T) {
			probe := &operationProbe{}
			var registry commands.Registry
			must(t, commands.RegisterOperation[*namedOperation](&registry, func(context.Context, *execution.Scope) (*operationProbe, error) { return probe, nil }))
			must(t, commands.RegisterOperation[namedOperation](&registry, func(context.Context, *execution.Scope) (*operationProbe, error) { return probe, nil }))
			switch kind {
			case "nil concrete":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (*namedOperation, error) { return nil, nil })))
			case "nil interface":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Operation, error) { return nil, nil })))
			case "empty batch":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Operations, error) {
					return commands.Operations{}, nil
				})))
			case "single":
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Operation, error) {
					return namedOperation{"A"}, nil
				})))
			default:
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Handle(func(operationOutcomeCommand, context.Context) ([]any, error) {
					return []any{namedOperation{"hidden"}}, nil
				})))
			}
			result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationOutcomeCommand{})
			must(t, err)
			if !result.IsSuccess() {
				t.Fatal(result.Details())
			}
			if kind == "single" {
				assertOperationCalls(t, probe, "execute:A")
				operationSummary(t, result, commands.RecoveryNotNeeded, 1, 1, 0)
			} else if len(probe.calls) != 0 {
				t.Fatal(probe.calls)
			}
			_, present := result.Response()
			if present != (kind == "ordinary DTO collection") {
				t.Fatal("response boundary", present)
			}
		})
	}
}

func TestOperationDenyAndValidateNeverActivateStagesOrParticipants(t *testing.T) {
	for _, denied := range []bool{true, false} {
		t.Run(map[bool]string{true: "denied execute", false: "validate only"}[denied], func(t *testing.T) {
			calls := 0
			var registry commands.Registry
			must(t, commands.RegisterOperation[namedOperation](&registry, func(context.Context, *execution.Scope) (*operationProbe, error) {
				calls++
				return &operationProbe{}, nil
			}))
			must(t, registry.AddOperationExecutionScope("ordinary", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
				calls++
				return &operationScope{}, nil
			}))
			must(t, registry.AddOperationCommitParticipant("terminal", func(context.Context, *execution.Scope) (commands.OperationCommitParticipant, error) {
				calls++
				return &operationTerminal{}, nil
			}))
			options := []commands.Option[operationOutcomeCommand]{commands.WithProvide(func(operationOutcomeCommand, context.Context) (int, error) { calls++; return 1, nil }, func(operationOutcomeCommand, context.Context, int) (commands.Operations, error) {
				calls++
				return commands.Operations{}, nil
			})}
			if denied {
				options = append(options, commands.WithAuthorization[operationOutcomeCommand](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"admin"}}}}))
			}
			must(t, commands.Register[operationOutcomeCommand](&registry, options...))
			pipeline := build(t, &registry, commands.PipelineOptions{})
			if denied {
				result, err := pipeline.Execute(t.Context(), operationOutcomeCommand{})
				must(t, err)
				if result.IsAuthorized() {
					t.Fatal("denied command authorized")
				}
			} else {
				result, err := pipeline.Validate(t.Context(), operationOutcomeCommand{})
				must(t, err)
				if !result.IsSuccess() {
					t.Fatal(result.Details())
				}
			}
			if calls != 0 {
				t.Fatal("business stages activated", calls)
			}
		})
	}
}

func TestOperationScopeCompatibilityFailsBeforeAnyActivationEvenEmpty(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		var registry commands.Registry
		must(t, commands.Register[operationBatchCommand](&registry, commands.Handle(operationBatchCommand.Handle)))
		if terminal {
			must(t, registry.AddDeferredCommitParticipant("legacy", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
				t.Fatal("activated")
				return nil, nil
			}))
		} else {
			must(t, registry.AddExecutionScope("legacy", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
				t.Fatal("activated")
				return nil, nil
			}))
		}
		if _, err := registry.Build(commands.PipelineOptions{}); !errors.Is(err, commands.ErrInvalidOperation) {
			t.Fatal(err)
		}
	}
}

func TestOperationNestingStickyBoundAndOriginalPipeline(t *testing.T) {
	for _, phase := range []string{"prepare", "handle", "execute", "compensate", "ordinary parent"} {
		for _, originalPipeline := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: " bound", true: " original"}[originalPipeline], func(t *testing.T) {
				probe := &operationProbe{}
				var registry commands.Registry
				registerOperations(t, &registry, probe)
				childCalls := 0
				must(t, commands.Register[ordinaryOperationChild](&registry, commands.Void(func(ordinaryOperationChild, context.Context) error { childCalls++; return nil })))
				var pipeline commands.Pipeline
				var callbackExecutor commands.Pipeline
				invokeChild := func(ctx context.Context) {
					executor := callbackExecutor
					if originalPipeline {
						executor = pipeline
					}
					_, _ = executor.Execute(ctx, ordinaryOperationChild{})
				}
				forwardFailure := errors.New("original")
				if phase == "execute" {
					probe.forward = func(ctx context.Context, _ string) error { invokeChild(ctx); return nil }
				}
				if phase == "compensate" {
					probe.forward = func(context.Context, string) error { return forwardFailure }
					probe.reverse = func(ctx context.Context, _ string, _ commands.OperationFailure) error { invokeChild(ctx); return nil }
				}
				// The bundle captures only the joined pipeline capability for this
				// test. Real execution dependencies must not retain expiring views.
				must(t, commands.Register[operationOutcomeCommand](&registry, commands.Prepare(func(ctx context.Context, inv *commands.Invocation, _ operationOutcomeCommand) (commands.Preparation[int], error) {
					callbackExecutor = inv.Pipeline()
					if phase == "prepare" {
						invokeChild(ctx)
					}
					return commands.Provided(1), nil
				}, func(ctx context.Context, inv *commands.Invocation, _ operationOutcomeCommand, _ int) (commands.Operations, error) {
					callbackExecutor = inv.Pipeline()
					if phase == "handle" {
						invokeChild(ctx)
					}
					return operationBatch(t, namedOperation{"A"}, namedOperation{"B"}), nil
				})))
				if phase == "ordinary parent" {
					must(t, commands.Register[Clear](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Clear) (commands.NoResponse, error) {
						executor := inv.Pipeline()
						if originalPipeline {
							executor = pipeline
						}
						_, _ = executor.Execute(ctx, operationOutcomeCommand{})
						return commands.NoResponse{}, nil
					})))
				}
				pipeline = build(t, &registry, commands.PipelineOptions{})
				var command any = operationOutcomeCommand{}
				if phase == "ordinary parent" {
					command = Clear{}
				}
				result, err := pipeline.Execute(t.Context(), command)
				if result.IsSuccess() || err == nil || childCalls != 0 {
					t.Fatal(result.Details(), err, childCalls)
				}
				if phase == "compensate" {
					if !errors.Is(err, forwardFailure) || errors.Is(err, commands.ErrInvalidOperation) {
						t.Fatal("recovery changed original cause", err)
					}
					operationSummary(t, result, commands.RecoveryIncomplete, 1, 0, 0)
				} else if phase == "execute" {
					operationSummary(t, result, commands.RecoveryCompleted, 1, 1, 1)
				} else if len(probe.calls) != 0 {
					t.Fatal(probe.calls)
				}
			})
		}
	}
}

func TestOperationExplicitCommitGuardIsSticky(t *testing.T) {
	var registry commands.Registry
	must(t, commands.Register[operationOutcomeCommand](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ operationOutcomeCommand) (commands.Operations, error) {
		if !errors.Is(inv.Execution().CheckExplicitCommit(ctx), commands.ErrInvalidOperation) {
			t.Fatal("guard allowed commit")
		}
		return commands.Operations{}, nil
	})))
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationOutcomeCommand{})
	if !errors.Is(err, commands.ErrInvalidOperation) || result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
}

func TestOperationCommitObservationPreventsExternalEntry(t *testing.T) {
	for _, disposition := range []commands.CommitDisposition{commands.Committed, commands.OutcomeUnknown, commands.MixedCommit} {
		probe := &operationProbe{}
		var registry commands.Registry
		registerOperations(t, &registry, probe)
		terminal := &operationTerminal{probe: probe, before: commands.CompletionReport{Disposition: disposition}, report: commands.CompletionReport{Disposition: disposition}}
		addOperationTerminal(t, &registry, terminal)
		result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
		if !errors.Is(err, commands.ErrInvalidOperation) || result.IsSuccess() {
			t.Fatal(result.Details(), err)
		}
		assertOperationCalls(t, probe, "begin:terminal", "complete:terminal")
		operationSummary(t, result, commands.RecoveryNotNeeded, 0, 0, 0)
	}
}

func TestOperationCancellationDetachedMetadataAndCompletedReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = identity.WithPrincipal(ctx, identity.Principal{})
	tenant, parseErr := tenancy.ParseID("team")
	must(t, parseErr)
	ctx = tenancy.WithTenant(ctx, tenant)
	probe := &operationProbe{}
	probe.forward = func(ctx context.Context, _ string) error { cancel(); return nil }
	probe.reverse = func(ctx context.Context, _ string, failure commands.OperationFailure) error {
		if ctx.Err() != nil {
			t.Fatal("cleanup reused canceled request")
		}
		if _, present := identity.PrincipalFrom(ctx); !present {
			t.Fatal("principal presence lost")
		}
		if tenant, present := tenancy.TenantFrom(ctx); !present || tenant.String() != "team" {
			t.Fatal(tenant, present)
		}
		snapshot, present := commands.ContextFrom(ctx)
		if !present || snapshot.ReceivedAt().IsZero() || snapshot.CorrelationID() != correlation.FromContext(ctx) {
			t.Fatal("metadata lost", snapshot)
		}
		deadline, present := ctx.Deadline()
		if !present || time.Until(deadline) < 29*time.Second {
			t.Fatal("default cleanup budget", deadline, present)
		}
		if failure.Source != commands.FailureCancellation || !failure.InvocationCompleted || failure.IsFailingInvocation {
			t.Fatal(failure)
		}
		return nil
	}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(ctx, operationBatchCommand{operationBatch(t, namedOperation{"A"}, namedOperation{"B"})})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertOperationCalls(t, probe, "execute:A", "compensate:A")
	operationSummary(t, result, commands.RecoveryCompleted, 1, 1, 1)
}

func TestOperationPanicAndCompensationFailurePreserveOriginal(t *testing.T) {
	probe := &operationProbe{forward: func(context.Context, string) error { panic("original private panic") }, reverse: func(context.Context, string, commands.OperationFailure) error { panic("private recovery panic") }}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
	var panicErr *execution.PanicError
	if !errors.As(err, &panicErr) || panicErr.Value != "original private panic" {
		t.Fatal(err)
	}
	operationSummary(t, result, commands.RecoveryIncomplete, 1, 0, 0)
	if !errors.As(result.OperationOutcomes()[0].CompensationFailure, &panicErr) || panicErr.Value != "private recovery panic" {
		t.Fatal(result.OperationOutcomes())
	}
	body, err := json.Marshal(result)
	must(t, err)
	for _, private := range []string{"original private", "private recovery", "recovery", "operationType", "operationOutcomes", "Name"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("private observation leaked: %s", body)
		}
	}
}

func TestOperationCompensationFailureContinuesAndMissingReversalIsIncomplete(t *testing.T) {
	original, recovery := errors.New("original"), errors.New("recovery")
	probe := &operationProbe{forward: func(_ context.Context, name string) error {
		if name == "C" {
			return original
		}
		return nil
	}, reverse: func(_ context.Context, name string, _ commands.OperationFailure) error {
		if name == "C" {
			return recovery
		}
		return nil
	}}
	var registry commands.Registry
	registerOperations(t, &registry, probe)
	result, err := build(t, &registry, commands.PipelineOptions{}).Execute(t.Context(), operationBatchCommand{operationBatch(t, irreversibleOperation{"A"}, namedOperation{"B"}, namedOperation{"C"})})
	if !errors.Is(err, original) || errors.Is(err, recovery) {
		t.Fatal(err)
	}
	assertOperationCalls(t, probe, "execute:A", "execute:B", "execute:C", "compensate:C", "compensate:B")
	operationSummary(t, result, commands.RecoveryIncomplete, 3, 2, 1)
	if result.OperationOutcomes()[0].Compensation != commands.CompensationNotAvailable || !errors.Is(result.OperationOutcomes()[2].CompensationFailure, recovery) {
		t.Fatal(result.OperationOutcomes())
	}
	if summary, _ := result.Recovery(); summary.FailedCompensationCount != 1 || summary.UncompensatedCount != 2 {
		t.Fatal(summary)
	}
}

func TestOperationRecoveryBudgetJoinsWithoutReentryOrLaterCallbacks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		original := errors.New("original")
		probe := &operationProbe{forward: func(_ context.Context, name string) error {
			if name == "B" {
				return original
			}
			return nil
		}}
		probe.reverse = func(ctx context.Context, _ string, _ commands.OperationFailure) error {
			<-ctx.Done()
			time.Sleep(time.Second)
			return nil
		}
		var registry commands.Registry
		registerOperations(t, &registry, probe)
		pipeline := build(t, &registry, commands.PipelineOptions{Operations: commands.OperationOptions{CompensationTimeout: time.Second}, OpenResources: func(context.Context) (execution.Resources, error) { return probe, nil }})
		started := time.Now()
		result, err := pipeline.Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"}, namedOperation{"B"})})
		if !errors.Is(err, original) || errors.Is(err, context.DeadlineExceeded) || time.Since(started) < 2*time.Second {
			t.Fatal("not joined or changed error", err)
		}
		assertOperationCalls(t, probe, "execute:A", "execute:B", "compensate:B", "dispose")
		operationSummary(t, result, commands.RecoveryIncomplete, 2, 1, 0)
		outcomes := result.OperationOutcomes()
		if outcomes[0].Compensation != commands.CompensationBudgetExpired || outcomes[1].Compensation != commands.CompensationFailed || !errors.Is(outcomes[1].CompensationFailure, context.DeadlineExceeded) {
			t.Fatal(outcomes)
		}
	})
}

func TestOperationScopeCompletionFailureUsesFreshRecoveryBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		original := errors.New("scope rejection")
		probe := &operationProbe{}
		var registry commands.Registry
		registerOperations(t, &registry, probe)
		terminal := &operationTerminal{probe: probe, complete: func(ctx context.Context, _ *commands.Invocation, _ commands.Result[any]) (commands.CompletionReport, error) {
			<-ctx.Done()
			return commands.CompletionReport{Disposition: commands.NotCommitted}, original
		}}
		addOperationTerminal(t, &registry, terminal)
		probe.reverse = func(ctx context.Context, _ string, failure commands.OperationFailure) error {
			if ctx.Err() != nil || failure.Source != commands.FailureScopeCompletion || !errors.Is(failure.Cause, original) {
				t.Fatal(ctx.Err(), failure)
			}
			return nil
		}
		result, err := build(t, &registry, commands.PipelineOptions{CleanupTimeout: time.Second, Operations: commands.OperationOptions{CompensationTimeout: time.Second}}).Execute(t.Context(), operationBatchCommand{operationBatch(t, namedOperation{"A"})})
		if !errors.Is(err, original) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		assertOperationCalls(t, probe, "begin:terminal", "execute:A", "complete:terminal", "compensate:A")
		operationSummary(t, result, commands.RecoveryCompleted, 1, 1, 1)
	})
}

func TestOperationHTTPPrivacyUsesActualPipelineAndRootBudgetForwarding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		probe := &operationProbe{forward: func(context.Context, string) error { return errors.New("private original") }, reverse: func(ctx context.Context, _ string, _ commands.OperationFailure) error {
			<-ctx.Done()
			return errors.New("private recovery")
		}}
		builder, err := arc.NewBuilder(arc.Options{CommandOperations: commands.OperationOptions{CompensationTimeout: time.Second}})
		must(t, err)
		must(t, commands.RegisterOperation[namedOperation](builder.Commands(), func(context.Context, *execution.Scope) (*operationProbe, error) { return probe, nil }))
		must(t, commands.Register[operationOutcomeCommand](builder, commands.Handle(func(operationOutcomeCommand, context.Context) (commands.Outcome[int], error) {
			return commands.Respond(0, namedOperation{"private payload"}), nil
		}), commands.WithOperations[operationOutcomeCommand](), commands.WithPath[operationOutcomeCommand]("/operation")))
		application, err := builder.Build()
		must(t, err)
		must(t, application.Start(t.Context()))
		t.Cleanup(func() { must(t, application.Shutdown(context.Background())) })
		request := httptest.NewRequest("POST", "/operation", strings.NewReader(`{}`))
		request.Header.Set("x-correlation-id", "12345678-1234-1234-1234-123456789012")
		response := httptest.NewRecorder()
		started := time.Now()
		application.ServeHTTP(response, request)
		if response.Code != 500 || time.Since(started) != time.Second {
			t.Fatal(response.Code, time.Since(started), response.Body.String())
		}
		var envelope map[string]any
		must(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		if len(envelope) != 9 || envelope["isSuccess"] != false {
			t.Fatal(envelope)
		}
		for _, private := range []string{"private", "recovery", "operationOutcomes", "operationType", "response"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatal(response.Body.String())
			}
		}
	})
}
