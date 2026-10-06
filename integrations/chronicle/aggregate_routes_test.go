// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	c "github.com/cratis/arc.go/integrations/chronicle"
)

type SecondAggregate struct {
	*c.AggregateRoot
	Name string
}

func TestIncompatibleAggregateRoutesOnSameSourceFailBeforeCommit(t *testing.T) {
	for _, distinctTypes := range []bool{false, true} {
		t.Run(map[bool]string{false: "same type different routes", true: "different aggregate types"}[distinctTypes], func(t *testing.T) {
			f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
			history := &historyReader{}
			builder, firstFactory := aggregateSetup(t, f, history)
			secondFactory, err := c.DefineAggregate(func(root *c.AggregateRoot) *Aggregate { return &Aggregate{AggregateRoot: root} },
				c.OnAggregateEvent(func(a *Aggregate, e Changed) error { a.Name = e.Name; return nil }),
				c.WithAggregateRoute[*Aggregate](c.Route{SourceType: "other", StreamType: "other", StreamID: "other"}))
			must(t, err)
			otherType, err := c.DefineAggregate(func(root *c.AggregateRoot) *SecondAggregate { return &SecondAggregate{AggregateRoot: root} },
				c.OnAggregateEvent(func(a *SecondAggregate, e Changed) error { a.Name = e.Name; return nil }))
			must(t, err)
			must(t, commands.Register[Change](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Change) (commands.NoResponse, error) {
				first, err := firstFactory.Get(ctx, inv)
				must(t, err)
				must(t, first.Apply(ctx, Changed{Name: "first"}))
				if distinctTypes {
					second, err := otherType.Get(ctx, inv)
					if !errors.Is(err, c.ErrMismatch) || second != nil {
						t.Fatal("incompatible route accepted", second, err)
					}
				} else {
					second, err := secondFactory.Get(ctx, inv)
					if !errors.Is(err, c.ErrMismatch) || second != nil {
						t.Fatal("incompatible route accepted", second, err)
					}
				}
				return commands.NoResponse{}, nil
			})))
			result, err := start(t, builder).Commands().Execute(t.Context(), Change{ID: "same"})
			if result.IsSuccess() || !errors.Is(err, c.ErrMismatch) || f.commits != 0 || f.rollbacks != 1 || history.reads != 2 || len(f.entries) != 0 {
				t.Fatal(result, f, history)
			}
		})
	}
}
