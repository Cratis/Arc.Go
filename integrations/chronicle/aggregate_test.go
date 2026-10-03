// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/validation"
)

type Aggregate struct {
	*c.AggregateRoot
	Name string
}
type historyReader struct {
	reads  int
	events []c.RecordedEvent
}

func (r *historyReader) ReadHistory(_ context.Context, request c.HistoryRequest) (c.History, error) {
	r.reads++
	expectation := c.Expectation{Kind: c.NoMatchingEvent}
	if len(r.events) > 0 {
		expectation = c.Expectation{Kind: c.UpperBound, Position: r.events[len(r.events)-1].Position}
	}
	return c.History{Events: r.events, Scope: c.LabeledScope{Label: string(request.Filter.Source), Filter: request.Filter, Expectation: expectation}}, nil
}
func aggregateSetup(t *testing.T, f *fakeFactory, h *historyReader) (*arc.Builder, *c.AggregateFactory[*Aggregate]) {
	builder, err := arc.NewBuilder(arc.Options{})
	must(t, err)
	integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "test", Namespace: "Default"}, nil
	}, Transactions: f, Events: catalog{}, History: h})
	must(t, err)
	must(t, integration.Install(builder))
	factory, err := c.DefineAggregate(func(root *c.AggregateRoot) *Aggregate { return &Aggregate{AggregateRoot: root} }, c.OnAggregateEvent(func(a *Aggregate, event Changed) error {
		if event.Name == "fail" {
			return errors.New("local fold failed")
		}
		a.Name = event.Name
		return nil
	}))
	must(t, err)
	return builder, factory
}
func TestAggregateLoadedScopeCachedStateAndCallbackExpiry(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
		history := &historyReader{}
		if loaded {
			history.events = []c.RecordedEvent{{Event: Changed{Name: "old"}, Type: c.EventType{ID: "changed", Generation: 1}, Position: 0}}
		}
		builder, factory := aggregateSetup(t, f, history)
		var stale *c.AggregateRoot
		must(t, commands.Register[Change](builder, commands.Prepare(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.Preparation[int], error) {
			aggregate, err := factory.Get(ctx, inv)
			if err != nil {
				return commands.Preparation[int]{}, err
			}
			stale = aggregate.AggregateRoot
			if aggregate.IsNew() == loaded {
				t.Fatal("wrong IsNew")
			}
			if loaded && aggregate.Name != "old" {
				t.Fatal(aggregate.Name)
			}
			return commands.Provided(0), nil
		}, func(ctx context.Context, inv *commands.Invocation, _ Change, _ int) (Changed, error) {
			if err := stale.Apply(ctx, Changed{}); !errors.Is(err, commands.ErrExecutionClosed) {
				t.Fatal(err)
			}
			aggregate, err := factory.Get(ctx, inv)
			if err != nil {
				return Changed{}, err
			}
			if err := aggregate.Apply(ctx, Changed{Name: "new"}); err != nil {
				return Changed{}, err
			}
			return Changed{Name: "returned"}, nil
		}), commands.WithNoResponse[Change]()))
		result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
		must(t, err)
		expected := c.NoMatchingEvent
		if loaded {
			expected = c.UpperBound
		}
		if !result.IsSuccess() || history.reads != 1 || f.commits != 1 || len(f.entries) != 2 || len(f.scopes) != 1 || f.scopes[0].Expectation.Kind != expected || f.scopes[0].Expectation.Position != 0 {
			t.Fatal(result, history, f)
		}
	}
}
func TestAggregateFailurePoisonsIgnoredMutationAndValidation(t *testing.T) {
	for _, validationFailure := range []bool{false, true} {
		f := &fakeFactory{}
		builder, factory := aggregateSetup(t, f, &historyReader{})
		must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.NoResponse, error) {
			aggregate, err := factory.Get(ctx, inv)
			if err != nil {
				return commands.NoResponse{}, err
			}
			if validationFailure {
				_ = aggregate.Failed("not allowed", validation.Error)
			} else {
				_ = aggregate.Apply(ctx, Changed{Name: "fail"})
			}
			return commands.NoResponse{}, nil
		})))
		result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
		if result.IsSuccess() || err == nil || f.commits != 0 || f.rollbacks != 1 {
			t.Fatal(result, err, f)
		}
	}
}
func TestAggregateEarlyCommitCannotCreateSuccessor(t *testing.T) {
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
	builder, factory := aggregateSetup(t, f, &historyReader{})
	must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.NoResponse, error) {
		aggregate, err := factory.Get(ctx, inv)
		if err != nil {
			return commands.NoResponse{}, err
		}
		must(t, aggregate.Apply(ctx, Changed{Name: "first"}))
		commit, err := aggregate.Commit(ctx)
		must(t, err)
		if commit.Report.Disposition != commands.Committed || f.commits != 1 {
			t.Fatal(commit, f)
		}
		if _, err = aggregate.Commit(ctx); !errors.Is(err, c.ErrClosed) {
			t.Fatal(err)
		}
		if err = aggregate.Apply(ctx, Changed{Name: "late"}); !errors.Is(err, c.ErrClosed) {
			t.Fatal(err)
		}
		return commands.NoResponse{}, nil
	})))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
	if result.IsSuccess() || !errors.Is(err, c.ErrClosed) || f.commits != 1 || len(f.entries) != 1 || result.Completion().Disposition != commands.Committed {
		t.Fatal(result, err, f)
	}
}
func TestEarlyAggregateCommitCannotIgnoreNestedLookupFailure(t *testing.T) {
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
	builder, factory := aggregateSetup(t, f, &historyReader{})
	must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.NoResponse, error) {
		aggregate, err := factory.Get(ctx, inv)
		if err != nil {
			return commands.NoResponse{}, err
		}
		must(t, aggregate.Apply(ctx, Changed{Name: "must not persist"}))
		_, _ = inv.Pipeline().Execute(ctx, struct{ Unknown string }{})
		if _, err = aggregate.Commit(ctx); !errors.Is(err, commands.ErrExecutionFailed) || !errors.Is(err, commands.ErrMissingHandler) {
			t.Fatal(err)
		}
		return commands.NoResponse{}, nil
	})))
	result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "a"})
	if result.IsSuccess() || err == nil || f.commits != 0 || f.rollbacks != 1 || len(f.entries) != 0 {
		t.Fatal(result, err, f)
	}
}

func TestAggregateDuplicateHandlersRejected(t *testing.T) {
	handler := c.OnAggregateEvent(func(*Aggregate, Changed) error { return nil })
	_, err := c.DefineAggregate(func(root *c.AggregateRoot) *Aggregate { return &Aggregate{AggregateRoot: root} }, handler, handler)
	if !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
}
