// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/internal/appendorigin"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go/eventsequences"
)

// operationProvider is one fake Chronicle provider: transactions, attributed
// immediate-append observation and the operation's borrowed recovery service.
type operationProvider struct {
	mu            sync.Mutex
	log           []string
	subscriptions map[eventsequences.Origin]func(c.CommitResult, error)
	commit        c.CommitResult
	commitErr     error
}

func newOperationProvider(commit commands.CommitDisposition) *operationProvider {
	return &operationProvider{subscriptions: map[eventsequences.Origin]func(c.CommitResult, error){}, commit: c.CommitResult{Report: commands.CompletionReport{Disposition: commit}}}
}
func (p *operationProvider) record(step string) {
	p.mu.Lock()
	p.log = append(p.log, step)
	p.mu.Unlock()
}
func (p *operationProvider) steps() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.log, ",")
}
func (p *operationProvider) count(step string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, value := range p.log {
		if value == step {
			n++
		}
	}
	return n
}
func (p *operationProvider) NewAppendOrigin() any { return eventsequences.NewOrigin() }
func (p *operationProvider) Subscribe(ctx context.Context, _ c.Coordinates, _ correlation.ID, notify func(c.CommitResult, error)) (func(), error) {
	origin, ok := appendorigin.From(ctx).(eventsequences.Origin)
	if !ok || origin == (eventsequences.Origin{}) {
		return nil, c.ErrInvalid
	}
	p.record("subscribe")
	p.mu.Lock()
	p.subscriptions[origin] = notify
	p.mu.Unlock()
	return func() {
		p.record("unsubscribe")
		p.mu.Lock()
		delete(p.subscriptions, origin)
		p.mu.Unlock()
	}, nil
}
func (p *operationProvider) Begin(context.Context, c.Coordinates) (c.Participant, c.CompletionOwner, error) {
	p.record("begin")
	return p, p, nil
}
func (p *operationProvider) Stage(context.Context, c.Batch) error { p.record("stage"); return nil }
func (p *operationProvider) Commit(context.Context) (c.CommitResult, error) {
	p.record("commit")
	return p.commit, p.commitErr
}
func (p *operationProvider) Rollback() error { p.record("rollback"); return nil }

// Append mimics an SDK immediate append attributed by the current callback snapshot.
func (p *operationProvider) Append(ctx context.Context, disposition commands.CommitDisposition) error {
	command, _ := commands.ContextFrom(ctx)
	value, _ := command.Values().Get(appendorigin.Name)
	origin, _ := value.(eventsequences.Origin)
	p.record("append")
	p.mu.Lock()
	notify := p.subscriptions[origin]
	p.mu.Unlock()
	var err error
	if disposition != commands.Committed {
		err = errors.New("immediate append not confirmed")
	}
	if notify != nil && origin != (eventsequences.Origin{}) {
		notify(c.CommitResult{Report: commands.CompletionReport{Disposition: disposition}}, err)
	}
	return err
}

func dispositionName(d commands.CommitDisposition) string {
	return [...]string{"NoPersistedWork", "NotCommitted", "Committed", "OutcomeUnknown", "MixedCommit"}[d]
}

var errOperationFailed = errors.New("operation failed")

type seatReservation struct {
	Fail      bool
	Immediate commands.CommitDisposition
}

func (seatReservation) CommandOperation() {}
func (o seatReservation) Execute(ctx context.Context, p *operationProvider) error {
	p.record("execute")
	if o.Immediate != commands.NoPersistedWork {
		_ = p.Append(ctx, o.Immediate)
	}
	if o.Fail {
		return errOperationFailed
	}
	return nil
}
func (seatReservation) Compensate(_ context.Context, p *operationProvider, failure commands.OperationFailure) error {
	p.record("compensate:" + dispositionName(failure.Completion.Disposition))
	return nil
}

type OperationBooking struct {
	ID               c.EventSourceID `json:"id"`
	Deferred         bool            `json:"deferred"`
	Invalid          bool            `json:"invalid"`
	BeforeOps        commands.CommitDisposition
	ValidationAppend commands.CommitDisposition
	Operation        seatReservation
	NoOps            bool
}

func operationSetup(t *testing.T, p *operationProvider, observe bool) *arc.Application {
	t.Helper()
	builder, err := arc.NewBuilder(arc.Options{Namespace: "Tests"})
	must(t, err)
	options := c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "test", Namespace: "Default"}, nil
	}, Transactions: p, Events: catalog{}}
	if observe {
		options.Appends = p
	}
	integration, err := c.New(options)
	must(t, err)
	must(t, integration.Install(builder))
	must(t, commands.RegisterOperation[seatReservation, *operationProvider](builder.Commands(), func(context.Context, *execution.Scope) (*operationProvider, error) {
		p.record("dependencies")
		return p, nil
	}))
	must(t, commands.Register[OperationBooking](builder, commands.Invoke(func(ctx context.Context, _ *commands.Invocation, command OperationBooking) (commands.Outcome[commands.NoResponse], error) {
		p.record("handle")
		if command.BeforeOps != commands.NoPersistedWork {
			_ = p.Append(ctx, command.BeforeOps)
		}
		var effects []any
		if command.Deferred {
			effects = append(effects, Changed{Name: "booked"})
		}
		if !command.NoOps {
			effects = append(effects, command.Operation)
		}
		return commands.Effects[commands.NoResponse](effects...), nil
	}), commands.WithOperations[OperationBooking](), commands.WithValidator[OperationBooking](validation.ValidatorFunc[OperationBooking](func(ctx context.Context, command OperationBooking) ([]validation.Result, error) {
		p.record("validate")
		if command.ValidationAppend != commands.NoPersistedWork {
			return nil, p.Append(ctx, command.ValidationAppend)
		}
		return nil, nil
	})), commands.WithValidator[OperationBooking](validation.ValidatorFunc[OperationBooking](func(_ context.Context, command OperationBooking) ([]validation.Result, error) {
		if command.Invalid {
			return []validation.Result{{Severity: validation.Error, Message: "invalid"}}, nil
		}
		return nil, nil
	}))))
	return start(t, builder)
}

func recovery(t *testing.T, result commands.Result[any]) commands.RecoverySummary {
	t.Helper()
	summary, present := result.Recovery()
	if !present {
		t.Fatal("operation command produced no recovery summary")
	}
	return summary
}

func TestOperationObservationIsOpenedAfterValidationAndBeforeBusinessCallbacks(t *testing.T) {
	p := newOperationProvider(commands.Committed)
	app := operationSetup(t, p, true)
	result, err := app.Commands().Execute(t.Context(), OperationBooking{ID: "a", Deferred: true})
	must(t, err)
	if !result.IsSuccess() || result.Completion().Disposition != commands.Committed || recovery(t, result).Status != commands.RecoveryNotNeeded {
		t.Fatal(result.Details(), result.Completion())
	}
	if got, want := p.steps(), "validate,subscribe,handle,dependencies,begin,stage,execute,unsubscribe,commit"; got != want {
		t.Fatalf("steps = %s, want %s", got, want)
	}
}

func TestOperationValidationFailureAndValidationOnlyNeverObserveOrBegin(t *testing.T) {
	p := newOperationProvider(commands.Committed)
	app := operationSetup(t, p, true)
	result, err := app.Commands().Execute(t.Context(), OperationBooking{ID: "a", Deferred: true, Invalid: true})
	if result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
	validated, err := app.Commands().Validate(t.Context(), OperationBooking{ID: "a", Deferred: true})
	if err != nil || !validated.IsSuccess() {
		t.Fatal(validated.Details(), err)
	}
	for _, step := range []string{"subscribe", "begin", "handle", "execute", "commit", "rollback"} {
		if p.count(step) != 0 {
			t.Fatalf("%s ran: %s", step, p.steps())
		}
	}
}

func TestApplicationValidatorAppendsBeforeBeginAreOutsideObservation(t *testing.T) {
	p := newOperationProvider(commands.Committed)
	app := operationSetup(t, p, true)
	result, err := app.Commands().Execute(t.Context(), OperationBooking{ID: "a",
		ValidationAppend: commands.Committed, Operation: seatReservation{Fail: true}})
	if result.IsSuccess() || !errors.Is(err, errOperationFailed) || result.Completion().Disposition != commands.NoPersistedWork {
		t.Fatal(result.Details(), result.Completion(), err)
	}
	// The direct application write happened before publication/subscription, so
	// the integration cannot include it in the completion or suppress recovery.
	if got, want := p.steps(), "validate,append,subscribe,handle,dependencies,execute,unsubscribe,compensate:NoPersistedWork"; got != want {
		t.Fatalf("steps = %s, want %s", got, want)
	}
	if recovery(t, result).Status != commands.RecoveryCompleted {
		t.Fatal(result.OperationOutcomes())
	}
}

func TestOperationCompletionDispositionsDriveRecovery(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		commit   commands.CommitDisposition
		command  OperationBooking
		want     commands.CommitDisposition
		recovery commands.RecoveryStatus
		steps    string
	}{
		{"no persisted work compensates", commands.Committed, OperationBooking{ID: "a", Operation: seatReservation{Fail: true}}, commands.NoPersistedWork, commands.RecoveryCompleted,
			"validate,subscribe,handle,dependencies,execute,unsubscribe,compensate:NoPersistedWork"},
		{"rejected deferred commit compensates after the terminal step", commands.NotCommitted, OperationBooking{ID: "a", Deferred: true}, commands.NotCommitted, commands.RecoveryCompleted,
			"validate,subscribe,handle,dependencies,begin,stage,execute,unsubscribe,commit,compensate:NotCommitted"},
		{"confirmed immediate append suppresses compensation", commands.Committed, OperationBooking{ID: "a", Operation: seatReservation{Fail: true, Immediate: commands.Committed}}, commands.Committed, commands.RecoverySuppressed,
			"validate,subscribe,handle,dependencies,execute,append,unsubscribe"},
		{"lost immediate acknowledgement is indeterminate", commands.Committed, OperationBooking{ID: "a", Operation: seatReservation{Fail: true, Immediate: commands.OutcomeUnknown}}, commands.OutcomeUnknown, commands.RecoveryIndeterminate,
			"validate,subscribe,handle,dependencies,execute,append,unsubscribe"},
		{"confirmed immediate and rolled back deferred is mixed", commands.NotCommitted, OperationBooking{ID: "a", Deferred: true, Operation: seatReservation{Immediate: commands.Committed}}, commands.MixedCommit, commands.RecoveryIndeterminate,
			"validate,subscribe,handle,dependencies,begin,stage,execute,append,unsubscribe,commit"},
		{"unconfirmed deferred commit is never redispatched", commands.OutcomeUnknown, OperationBooking{ID: "a", Deferred: true}, commands.OutcomeUnknown, commands.RecoveryIndeterminate,
			"validate,subscribe,handle,dependencies,begin,stage,execute,unsubscribe,commit"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			p := newOperationProvider(scenario.commit)
			app := operationSetup(t, p, true)
			result, err := app.Commands().Execute(t.Context(), scenario.command)
			if result.IsSuccess() || err == nil {
				t.Fatal("operation command succeeded", result.Details())
			}
			if result.Completion().Disposition != scenario.want {
				t.Fatalf("completion = %v, want %v (%v)", result.Completion().Disposition, scenario.want, err)
			}
			if status := recovery(t, result).Status; status != scenario.recovery {
				t.Fatalf("recovery = %v, want %v", status, scenario.recovery)
			}
			if got := p.steps(); got != scenario.steps {
				t.Fatalf("steps = %s, want %s", got, scenario.steps)
			}
		})
	}
}

func TestPreEntryImmediateHazardsRefuseOperationsBeforeEntry(t *testing.T) {
	for _, scenario := range []struct {
		immediate commands.CommitDisposition
		refused   bool
	}{
		{commands.NoPersistedWork, false},
		{commands.NotCommitted, false},
		{commands.Committed, true},
		{commands.OutcomeUnknown, true},
		{commands.MixedCommit, true},
	} {
		t.Run(dispositionName(scenario.immediate), func(t *testing.T) {
			p := newOperationProvider(commands.Committed)
			app := operationSetup(t, p, true)
			result, err := app.Commands().Execute(t.Context(), OperationBooking{ID: "a", Deferred: true, BeforeOps: scenario.immediate})
			executed := p.count("execute") == 1
			if scenario.refused {
				if result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) || executed || p.count("commit") != 0 || p.count("stage") != 0 {
					t.Fatal(result.Details(), err, p.steps())
				}
				if summary := recovery(t, result); summary.StartedCount != 0 || summary.Status != commands.RecoveryNotNeeded {
					t.Fatal(summary)
				}
				return
			}
			if !executed {
				t.Fatal("operation was not entered", p.steps(), err)
			}
			if scenario.immediate == commands.NotCommitted {
				// The recorded rejection poisons the owner: deferred work rolls back
				// and the entered operation is compensated against known facts.
				if result.IsSuccess() || p.count("rollback") != 1 || p.count("commit") != 0 || recovery(t, result).Status != commands.RecoveryCompleted {
					t.Fatal(result.Details(), err, p.steps())
				}
				return
			}
			if !result.IsSuccess() || err != nil {
				t.Fatal(result.Details(), err)
			}
		})
	}
}

func TestOperationsWithoutAttributedObservationFailClosed(t *testing.T) {
	p := newOperationProvider(commands.Committed)
	app := operationSetup(t, p, false)
	result, err := app.Commands().Execute(t.Context(), OperationBooking{ID: "a", Deferred: true})
	if result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) || !errors.Is(err, c.ErrUnsupported) {
		t.Fatal(result.Details(), err)
	}
	if p.count("execute") != 0 || p.count("begin") != 0 || p.count("stage") != 0 || p.count("commit") != 0 {
		t.Fatal(p.steps())
	}
}

func TestEmptyOperationCommandsUseTheOrdinaryTerminalPath(t *testing.T) {
	p := newOperationProvider(commands.Committed)
	app := operationSetup(t, p, true)
	result, err := app.Commands().Execute(t.Context(), OperationBooking{ID: "a", Deferred: true, NoOps: true})
	must(t, err)
	if !result.IsSuccess() || result.Completion().Disposition != commands.Committed || p.count("execute") != 0 || p.count("commit") != 1 {
		t.Fatal(result.Details(), p.steps())
	}
}

type ExplicitCommit struct {
	ID        c.EventSourceID `json:"id"`
	Propagate bool            `json:"propagate"`
	Empty     bool            `json:"empty"`
}

func TestExplicitAggregateCommitIsRefusedInOperationCommands(t *testing.T) {
	for _, scenario := range []struct {
		name             string
		propagate, empty bool
	}{{"ignored", false, false}, {"propagated", true, false}, {"empty operations", false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
			builder, factory := aggregateSetup(t, f, &historyReader{})
			p := newOperationProvider(commands.Committed)
			must(t, commands.RegisterOperation[seatReservation, *operationProvider](builder.Commands(), func(context.Context, *execution.Scope) (*operationProvider, error) { return p, nil }))
			var refusal error
			must(t, commands.Register[ExplicitCommit](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, command ExplicitCommit) (commands.Operations, error) {
				aggregate, err := factory.Get(ctx, inv)
				if err != nil {
					return commands.Operations{}, err
				}
				if err := aggregate.Apply(ctx, Changed{Name: "early"}); err != nil {
					return commands.Operations{}, err
				}
				_, refusal = aggregate.Commit(ctx)
				if command.Propagate {
					return commands.Operations{}, refusal
				}
				if command.Empty {
					return commands.Operations{}, nil
				}
				return commands.NewOperations(seatReservation{})
			}), commands.WithOperations[ExplicitCommit]()))
			result, err := start(t, builder).Commands().Execute(t.Context(), ExplicitCommit{ID: "a", Propagate: scenario.propagate, Empty: scenario.empty})
			if !errors.Is(refusal, commands.ErrInvalidOperation) || result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) {
				t.Fatal(refusal, result.Details(), err)
			}
			if _, present := result.Response(); present || f.commits != 0 || p.count("execute") != 0 || len(f.entries) != 0 {
				t.Fatal(f.commits, f.rollbacks, p.steps(), f.entries)
			}
		})
	}
}

func TestExplicitAggregateCommitInOrdinaryCommandsIsUnchanged(t *testing.T) {
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
	builder, factory := aggregateSetup(t, f, &historyReader{})
	must(t, commands.Register[ExplicitCommit](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ ExplicitCommit) (commands.NoResponse, error) {
		aggregate, err := factory.Get(ctx, inv)
		if err != nil {
			return commands.NoResponse{}, err
		}
		if err := aggregate.Apply(ctx, Changed{Name: "early"}); err != nil {
			return commands.NoResponse{}, err
		}
		_, err = aggregate.Commit(ctx)
		return commands.NoResponse{}, err
	})))
	result, err := start(t, builder).Commands().Execute(t.Context(), ExplicitCommit{ID: "a"})
	must(t, err)
	if !result.IsSuccess() || f.commits != 1 || !slices.ContainsFunc(f.entries, func(e c.Entry) bool { return e.Event.(Changed).Name == "early" }) {
		t.Fatal(result.Details(), f)
	}
}
