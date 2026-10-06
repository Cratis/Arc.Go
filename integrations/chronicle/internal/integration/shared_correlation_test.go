//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
)

type ConcurrentAuthor struct {
	ID     integration.EventSourceID `json:"id"`
	First  bool                      `json:"first"`
	Reject bool                      `json:"reject"`
}

func TestConcurrentSameCorrelationKeepsOwnerCommitOutcomesSeparate(t *testing.T) {
	for _, firstRejects := range []bool{true, false} {
		t.Run(map[bool]string{true: "rejected first", false: "committed first"}[firstRejects], func(t *testing.T) {
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
			secondEntered := make(chan struct{})
			firstFinished := make(chan struct{})
			require(t, commands.Register[ConcurrentAuthor](builder, commands.Handle(func(command ConcurrentAuthor, ctx context.Context) (AuthorCreated, error) {
				wait := firstFinished
				if command.First {
					wait = secondEntered
				} else {
					close(secondEntered)
				}
				select {
				case <-wait:
				case <-ctx.Done():
					return AuthorCreated{}, ctx.Err()
				}
				name := "unique"
				if command.Reject {
					name = "taken"
				}
				return AuthorCreated{Name: name}, nil
			}), commands.WithNoResponse[ConcurrentAuthor]()))
			app, err := builder.Build()
			require(t, err)
			require(t, app.Start(ctx))
			defer func() { require(t, app.Shutdown(context.Background())) }()
			id, err := concepts.NewUUID()
			require(t, err)
			ctx = correlation.WithID(ctx, id)
			type execution struct {
				result commands.Result[any]
				err    error
				source string
				reject bool
			}
			results := make(chan execution, 2)
			go func() {
				result, err := app.Commands().Execute(ctx, ConcurrentAuthor{ID: "first", First: true, Reject: firstRejects})
				results <- execution{result, err, "first", firstRejects}
				close(firstFinished)
			}()
			go func() {
				result, err := app.Commands().Execute(ctx, ConcurrentAuthor{ID: "second", Reject: !firstRejects})
				results <- execution{result, err, "second", !firstRejects}
			}()
			for range 2 {
				got := <-results
				persisted := len(history(t, ctx, client, storeName, "Default", got.source))
				if got.reject {
					if got.result.IsSuccess() || got.err == nil || got.result.Completion().Disposition != commands.NotCommitted || persisted != 0 {
						t.Errorf("rejected %s = %+v, %v; completion = %+v, persisted = %d", got.source, got.result.Details(), got.err, got.result.Completion(), persisted)
					}
				} else if !got.result.IsSuccess() || got.err != nil || got.result.Completion().Disposition != commands.Committed || persisted != 1 {
					t.Errorf("accepted %s = %+v, %v; completion = %+v, persisted = %d", got.source, got.result.Details(), got.err, got.result.Completion(), persisted)
				}
			}
		})
	}
}
