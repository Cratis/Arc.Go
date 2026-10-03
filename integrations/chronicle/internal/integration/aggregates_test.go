//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
)

type AuthorAggregate struct {
	*integration.AggregateRoot
	Name string
}
type SecondAuthorAggregate struct {
	*integration.AggregateRoot
	Name string
}

type RaceAuthor struct {
	ID     integration.EventSourceID `json:"id"`
	Target string                    `json:"target"`
}

func TestIncompatibleAggregateRoutesOnSameSourceRejectWithoutPersistence(t *testing.T) {
	client, store, ctx := clientFor(t, func(registry *chronicle.Registry) {
		_, err := chronicle.RegisterEvent[AuthorCreated](registry)
		require(t, err)
	})
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: store})
	require(t, err)
	require(t, adapter.Install(builder))
	first, err := integration.DefineAggregate(func(root *integration.AggregateRoot) *AuthorAggregate {
		return &AuthorAggregate{AggregateRoot: root}
	}, integration.OnAggregateEvent(func(a *AuthorAggregate, e AuthorCreated) error { a.Name = e.Name; return nil }))
	require(t, err)
	second, err := integration.DefineAggregate(func(root *integration.AggregateRoot) *SecondAuthorAggregate {
		return &SecondAuthorAggregate{AggregateRoot: root}
	}, integration.OnAggregateEvent(func(a *SecondAuthorAggregate, e AuthorCreated) error { a.Name = e.Name; return nil }))
	require(t, err)
	require(t, commands.Register[CreatePair](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ CreatePair) (commands.NoResponse, error) {
		a, err := first.Get(ctx, inv)
		require(t, err)
		require(t, a.Apply(ctx, AuthorCreated{Name: "must not persist"}))
		b, err := second.Get(ctx, inv)
		if !errors.Is(err, integration.ErrMismatch) || b != nil {
			t.Fatal("incompatible route accepted", b, err)
		}
		return commands.NoResponse{}, nil
	})))
	app, err := builder.Build()
	require(t, err)
	require(t, app.Start(ctx))
	t.Cleanup(func() { require(t, app.Shutdown(context.Background())) })
	result, err := app.Commands().Execute(ctx, CreatePair{ID: "same"})
	records := history(t, ctx, client, store, "Default", "same")
	if result.IsSuccess() || !errors.Is(err, integration.ErrMismatch) || result.Completion().Disposition != commands.NotCommitted || len(records) != 0 {
		t.Fatal(result, err, records)
	}
}

func TestCompetingAggregateDecisionsAreAtomicAcrossTargets(t *testing.T) {
	client, store, ctx := clientFor(t, func(registry *chronicle.Registry) {
		_, err := chronicle.RegisterEvent[AuthorCreated](registry)
		require(t, err)
	})
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: store})
	require(t, err)
	require(t, adapter.Install(builder))
	factory, err := integration.DefineAggregate(func(root *integration.AggregateRoot) *AuthorAggregate { return &AuthorAggregate{AggregateRoot: root} }, integration.OnAggregateEvent(func(a *AuthorAggregate, e AuthorCreated) error { a.Name = e.Name; return nil }))
	require(t, err)
	var loaded sync.WaitGroup
	loaded.Add(2)
	require(t, commands.Register[RaceAuthor](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, c RaceAuthor) (integration.EventBatch, error) {
		aggregate, err := factory.Get(ctx, inv)
		if err != nil {
			loaded.Done()
			return integration.EventBatch{}, err
		}
		if !aggregate.IsNew() {
			t.Error("decision did not read empty history")
		}
		loaded.Done()
		loaded.Wait()
		if err := aggregate.Apply(ctx, AuthorCreated{Name: c.Target}); err != nil {
			return integration.EventBatch{}, err
		}
		return integration.Events(integration.EventForSource(integration.EventSourceID(c.Target), AuthorCreated{Name: "side"})), nil
	}), commands.WithNoResponse[RaceAuthor]()))
	app, err := builder.Build()
	require(t, err)
	require(t, app.Start(ctx))
	defer func() { require(t, app.Shutdown(context.Background())) }()
	type outcome struct {
		target string
		result commands.Result[any]
		err    error
	}
	finished := make(chan outcome, 2)
	for _, target := range []string{"A", "B"} {
		go func() {
			result, err := app.Commands().Execute(ctx, RaceAuthor{ID: "same", Target: target})
			finished <- outcome{target, result, err}
		}()
	}
	successes := 0
	for range 2 {
		outcome := <-finished
		records := history(t, ctx, client, store, "Default", outcome.target)
		if outcome.result.IsSuccess() {
			require(t, outcome.err)
			successes++
			if len(records) != 1 {
				t.Fatal(records)
			}
		} else {
			if outcome.err == nil || len(records) != 0 {
				t.Fatal("rejected target persisted", outcome, records)
			}
			findings := outcome.result.Details().ValidationResults
			if len(findings) != 1 || findings[0].Reason != validation.ConcurrencyViolation {
				t.Fatal(outcome.result.Details(), outcome.err)
			}
		}
	}
	if successes != 1 || len(history(t, ctx, client, store, "Default", "same")) != 1 {
		t.Fatal("competing decisions did not reject atomically", successes)
	}
}
