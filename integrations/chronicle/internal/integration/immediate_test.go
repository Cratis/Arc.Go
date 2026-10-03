//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"errors"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go/events"
)

type ImmediateAuthor struct {
	ID     integration.EventSourceID `json:"id"`
	Reject bool                      `json:"reject"`
}

func TestIgnoredImmediateRejectionRollsBackDeferredWorkAndPreservesCommittedFacts(t *testing.T) {
	client, storeName, ctx := authorClient(t)
	store, err := client.EventStore(ctx, storeName)
	require(t, err)
	seed, err := store.EventLog().Append(ctx, "seed", AuthorCreated{Name: "taken"})
	require(t, err)
	require(t, seed.Err())
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: storeName})
	require(t, err)
	require(t, adapter.Install(builder))
	cause := errors.New("failure after immediate commit")
	require(t, commands.Register[ImmediateAuthor](builder, commands.Handle(func(c ImmediateAuthor, ctx context.Context) (AuthorCreated, error) {
		sequence, err := store.EventSequence(events.EventLog)
		if err != nil {
			return AuthorCreated{}, err
		}
		if c.Reject {
			_, _ = sequence.Append(ctx, "rejected-immediate", AuthorCreated{Name: "taken"})
			return AuthorCreated{Name: "deferred"}, nil
		}
		result, err := sequence.Append(ctx, "committed-immediate", AuthorCreated{Name: "unique"})
		if err != nil {
			return AuthorCreated{}, err
		}
		if err := result.Err(); err != nil {
			return AuthorCreated{}, err
		}
		return AuthorCreated{}, cause
	}), commands.WithNoResponse[ImmediateAuthor]()))
	app, err := builder.Build()
	require(t, err)
	require(t, app.Start(ctx))
	defer func() { require(t, app.Shutdown(context.Background())) }()
	rejected, err := app.Commands().Execute(ctx, ImmediateAuthor{ID: "deferred", Reject: true})
	if rejected.IsSuccess() || err == nil || len(rejected.Details().ValidationResults) != 1 || rejected.Details().ValidationResults[0].Reason != validation.ConstraintViolation {
		t.Fatal(rejected.Details(), err)
	}
	if len(history(t, ctx, client, storeName, "Default", "deferred")) != 0 || len(history(t, ctx, client, storeName, "Default", "rejected-immediate")) != 0 {
		t.Fatal("failed batch persisted")
	}
	committed, err := app.Commands().Execute(ctx, ImmediateAuthor{ID: "unused"})
	if committed.IsSuccess() || !errors.Is(err, cause) || committed.Completion().Disposition != commands.Committed || len(history(t, ctx, client, storeName, "Default", "committed-immediate")) != 1 {
		t.Fatal(committed.Details(), err)
	}
}
