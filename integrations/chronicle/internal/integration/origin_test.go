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
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
)

func TestImmediateOtherCoordinatesAndZeroOriginAreNotAttributed(t *testing.T) {
	for _, boundary := range []string{"store", "namespace", "sequence", "zero origin"} {
		t.Run(boundary, func(t *testing.T) {
			client, storeName, ctx := authorClient(t)
			selectedStore, namespace, sequenceID := storeName, chronicle.DefaultNamespace, events.EventLog
			switch boundary {
			case "store":
				selectedStore = storeName + "-other"
			case "namespace":
				namespace = "other"
			case "sequence":
				sequenceID = "other"
			}
			store, err := client.EventStore(ctx, selectedStore, chronicle.WithNamespace(namespace))
			require(t, err)
			sequence, err := store.EventSequence(sequenceID)
			require(t, err)
			builder, err := arc.NewBuilder(arc.Options{})
			require(t, err)
			adapter, err := sdk.New(client, sdk.Config{Store: storeName})
			require(t, err)
			require(t, adapter.Install(builder))
			cause := errors.New("handler failure without selected persistence")
			require(t, commands.Register[ConcurrentAuthor](builder, commands.Handle(func(_ ConcurrentAuthor, callback context.Context) (commands.NoResponse, error) {
				appendContext := callback
				if boundary == "zero origin" {
					// Deliberately discard Arc's callback snapshot: no root origin is supplied.
					appendContext = ctx
				}
				result, err := sequence.Append(appendContext, "foreign", AuthorCreated{Name: "foreign"})
				if err != nil {
					return commands.NoResponse{}, err
				}
				if err := result.Err(); err != nil {
					return commands.NoResponse{}, err
				}
				return commands.NoResponse{}, cause
			})))
			app, err := builder.Build()
			require(t, err)
			require(t, app.Start(ctx))
			defer func() { require(t, app.Shutdown(context.Background())) }()
			result, err := app.Commands().Execute(ctx, ConcurrentAuthor{ID: "root"})
			if result.IsSuccess() || !errors.Is(err, cause) || result.Completion().Disposition != commands.NoPersistedWork {
				t.Fatal(result.Details(), err, result.Completion())
			}
			persisted, err := sequence.ReadSource(ctx, "foreign", eventsequences.SourceFilter{})
			require(t, err)
			if len(persisted) != 1 {
				t.Fatal(persisted)
			}
		})
	}
}

func TestConcurrentSameCorrelationImmediateFailureIsAttributedOnlyToItsRoot(t *testing.T) {
	for _, rejectFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "rejected first", false: "committed first"}[rejectFirst], func(t *testing.T) {
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
			entered := make(chan eventsequences.Origin, 2)
			bothEntered, firstDone := make(chan struct{}), make(chan struct{})
			require(t, commands.Register[ConcurrentAuthor](builder, commands.Handle(func(command ConcurrentAuthor, ctx context.Context) (AuthorCreated, error) {
				origin, handled, err := sdk.ResolveAppendOrigin(ctx)
				if err != nil || !handled || origin == (eventsequences.Origin{}) {
					return AuthorCreated{}, errors.New("missing executing origin")
				}
				entered <- origin
				wait := firstDone
				if command.First {
					wait = bothEntered
				}
				select {
				case <-wait:
				case <-ctx.Done():
					return AuthorCreated{}, ctx.Err()
				}
				name := "accepted-immediate"
				if command.Reject {
					name = "taken"
				}
				// Deliberately ignored rejection must poison only the rejecting root.
				_, _ = store.EventLog().Append(ctx, events.SourceID("immediate-"+string(command.ID)), AuthorCreated{Name: name})
				return AuthorCreated{Name: "deferred-" + string(command.ID)}, nil
			}), commands.WithNoResponse[ConcurrentAuthor]()))
			app, err := builder.Build()
			require(t, err)
			require(t, app.Start(ctx))
			defer func() { require(t, app.Shutdown(context.Background())) }()
			inherited := eventsequences.NewOrigin()
			ctx = eventsequences.WithOrigin(correlation.WithID(ctx, correlation.ID{1}), inherited)
			type outcome struct {
				result commands.Result[any]
				err    error
				source string
				reject bool
			}
			results := make(chan outcome, 2)
			go func() {
				result, err := app.Commands().Execute(ctx, ConcurrentAuthor{ID: "first", First: true, Reject: rejectFirst})
				results <- outcome{result, err, "first", rejectFirst}
				close(firstDone)
			}()
			go func() {
				result, err := app.Commands().Execute(ctx, ConcurrentAuthor{ID: "second", Reject: !rejectFirst})
				results <- outcome{result, err, "second", !rejectFirst}
			}()
			var first, second eventsequences.Origin
			for index := range 2 {
				select {
				case origin := <-entered:
					if index == 0 {
						first = origin
					} else {
						second = origin
					}
				case <-ctx.Done():
					t.Fatal("commands did not enter", ctx.Err())
				}
			}
			if first == second || first == inherited || second == inherited {
				t.Error("independent roots reused origin")
			}
			close(bothEntered)
			for range 2 {
				got := <-results
				immediate := len(history(t, ctx, client, storeName, "Default", "immediate-"+got.source))
				deferred := len(history(t, ctx, client, storeName, "Default", got.source))
				if got.reject {
					if got.result.IsSuccess() || got.err == nil || got.result.Completion().Disposition != commands.NotCommitted || immediate != 0 || deferred != 0 {
						t.Errorf("rejected %s: %+v, %v; completion=%+v, immediate=%d, deferred=%d", got.source, got.result.Details(), got.err, got.result.Completion(), immediate, deferred)
					}
				} else if !got.result.IsSuccess() || got.err != nil || got.result.Completion().Disposition != commands.Committed || immediate != 1 || deferred != 1 {
					t.Errorf("accepted %s: %+v, %v; completion=%+v, immediate=%d, deferred=%d", got.source, got.result.Details(), got.err, got.result.Completion(), immediate, deferred)
				}
			}
		})
	}
}
