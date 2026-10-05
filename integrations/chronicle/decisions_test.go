// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	c "github.com/cratis/arc.go/integrations/chronicle"
)

// decisionFactory is a fakeFactory whose participant enrolls decision evidence.
type decisionFactory struct {
	fakeFactory
	mu       sync.Mutex
	enrolled []any
	inFlight atomic.Bool
	overlap  atomic.Bool
	refuse   error
}

func (f *decisionFactory) Begin(context.Context, c.Coordinates) (c.Participant, c.CompletionOwner, error) {
	f.begins++
	return f, f, nil
}
func (f *decisionFactory) EnrollDecision(_ context.Context, evidence any) error {
	if !f.inFlight.CompareAndSwap(false, true) {
		f.overlap.Store(true)
	}
	defer f.inFlight.Store(false)
	if f.refuse != nil {
		return f.refuse
	}
	f.mu.Lock()
	f.enrolled = append(f.enrolled, evidence)
	f.mu.Unlock()
	return nil
}

type Decide struct {
	ID c.EventSourceID `json:"id"`
}

func TestDecisionEnrollmentBeginsOneTransactionAndSerializesConcurrentReads(t *testing.T) {
	f := &decisionFactory{fakeFactory: fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.NoPersistedWork}}}}
	builder, _ := setup(t, &f.fakeFactory)
	integration := newDecisionIntegration(t, f)
	must(t, integration.Install(builder))
	must(t, commands.Register[Decide](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Decide) (commands.NoResponse, error) {
		var wait sync.WaitGroup
		errs := make([]error, 8)
		for index := range errs {
			wait.Add(1)
			go func() {
				defer wait.Done()
				errs[index] = integration.EnrollDecision(ctx, inv, index)
			}()
		}
		wait.Wait()
		return commands.NoResponse{}, errors.Join(errs...)
	}), commands.WithNoResponse[Decide]()))
	result, err := start(t, builder).Commands().Execute(t.Context(), Decide{ID: "a"})
	if !result.IsSuccess() || err != nil {
		t.Fatal(result.Details(), err)
	}
	if f.begins != 1 || len(f.enrolled) != 8 || f.commits != 1 || f.rollbacks != 0 || f.overlap.Load() {
		t.Fatal("begins", f.begins, "enrolled", f.enrolled, "commits", f.commits, "rollbacks", f.rollbacks, "overlap", f.overlap.Load())
	}
}

func TestRefusedDecisionEnrollmentRollsBackIgnoredWork(t *testing.T) {
	refusal := errors.New("foreign token")
	for _, tc := range []struct {
		name  string
		build func(*fakeFactory, *decisionFactory) c.TransactionFactory
		want  error
	}{
		{"provider refusal", func(_ *fakeFactory, d *decisionFactory) c.TransactionFactory { d.refuse = refusal; return d }, refusal},
		{"participant without decision support", func(f *fakeFactory, _ *decisionFactory) c.TransactionFactory { return f }, c.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &decisionFactory{fakeFactory: fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}}
			factory := tc.build(&f.fakeFactory, f)
			builder, _ := setup(t, &f.fakeFactory)
			integration, err := c.New(c.Options{StoreResolver: testCoordinates, Transactions: factory, Events: catalog{}})
			must(t, err)
			must(t, integration.Install(builder))
			var enrollment error
			must(t, commands.Register[Decide](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Decide) (Changed, error) {
				// The application ignores the refusal; the transaction must not commit.
				enrollment = integration.EnrollDecision(ctx, inv, "evidence")
				return Changed{Name: "unguarded"}, nil
			}), commands.WithNoResponse[Decide]()))
			result, err := start(t, builder).Commands().Execute(t.Context(), Decide{ID: "a"})
			if !errors.Is(enrollment, tc.want) {
				t.Fatal("enrollment", enrollment)
			}
			if result.IsSuccess() || err == nil || f.commits != 0 || f.rollbacks != 1 || result.Completion().Disposition != commands.NotCommitted {
				t.Fatal(result.Details(), result.Completion(), err, "commits", f.commits, "rollbacks", f.rollbacks)
			}
		})
	}
}

func TestValidationOnlyDecisionEnrollmentNeverBegins(t *testing.T) {
	f := &decisionFactory{}
	builder, _ := setup(t, &f.fakeFactory)
	integration := newDecisionIntegration(t, f)
	must(t, integration.Install(builder))
	// Filters are the callbacks that run in validation-only execution.
	enrollment := errors.New("filter did not run")
	must(t, builder.Commands().AddFilter("decide", func(context.Context, *execution.Scope) (commands.Filter, error) {
		return commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			enrollment = integration.EnrollDecision(ctx, inv, "evidence")
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}), nil
	}))
	must(t, commands.Register[Decide](builder, commands.Handle(func(Decide, context.Context) (commands.NoResponse, error) {
		t.Error("validation ran Handle")
		return commands.NoResponse{}, nil
	}), commands.WithNoResponse[Decide]()))
	result, err := start(t, builder).Commands().Validate(t.Context(), Decide{ID: "a"})
	if !result.IsSuccess() || err != nil {
		t.Fatal(result.Details(), err)
	}
	if !errors.Is(enrollment, commands.ErrExecutionMismatch) || f.begins != 0 || len(f.enrolled) != 0 {
		t.Fatal(enrollment, f.begins, f.enrolled)
	}
}

func TestDecisionEnrollmentRejectsInvalidArguments(t *testing.T) {
	f := &decisionFactory{}
	integration := newDecisionIntegration(t, f)
	var missing *c.Integration
	for name, err := range map[string]error{
		"nil integration": missing.EnrollDecision(t.Context(), nil, "evidence"),
		"nil evidence":    integration.EnrollDecision(t.Context(), nil, nil),
		"nil context":     integration.EnrollDecision(nil, nil, "evidence"), //nolint:staticcheck // Deliberately exercise invalid context admission (SA1012).
	} {
		if !errors.Is(err, c.ErrInvalid) {
			t.Error(name, err)
		}
	}
	if err := integration.EnrollDecision(t.Context(), nil, "evidence"); !errors.Is(err, commands.ErrExecutionMismatch) {
		t.Fatal("no invocation", err)
	}
}

func testCoordinates(context.Context, commands.CommandContext) (c.Coordinates, error) {
	return c.Coordinates{Store: "test", Namespace: "Default"}, nil
}

func newDecisionIntegration(t *testing.T, f *decisionFactory) *c.Integration {
	t.Helper()
	integration, err := c.New(c.Options{StoreResolver: testCoordinates, Transactions: f, Events: catalog{}})
	must(t, err)
	return integration
}
