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
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
)

func TestGuardedEarlyAggregateCommitAgainstKernel(t *testing.T) {
	for _, scenario := range []string{"denied", "validation", "lookup", "nested success", "late failure"} {
		t.Run(scenario, func(t *testing.T) {
			client, store, ctx := clientFor(t, func(registry *chronicle.Registry) {
				_, err := chronicle.RegisterEvent[AuthorCreated](registry)
				require(t, err)
			})
			builder, err := arc.NewBuilder(arc.Options{})
			require(t, err)
			adapter, err := sdk.New(client, sdk.Config{Store: store})
			require(t, err)
			require(t, adapter.Install(builder))
			factory, err := integration.DefineAggregate(func(root *integration.AggregateRoot) *AuthorAggregate {
				return &AuthorAggregate{AggregateRoot: root}
			}, integration.OnAggregateEvent(func(a *AuthorAggregate, e AuthorCreated) error { a.Name = e.Name; return nil }))
			require(t, err)
			require(t, commands.Register[CreateAuthor](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, c CreateAuthor) (commands.NoResponse, error) {
				if scenario != "nested success" {
					t.Fatal("rejected child handled")
				}
				aggregate, err := factory.Get(ctx, inv)
				require(t, err)
				require(t, aggregate.Apply(ctx, AuthorCreated{Name: c.Name}))
				_, err = aggregate.Commit(ctx)
				return commands.NoResponse{}, err
			}), commands.WithAuthorization[CreateAuthor](metadata.Authorization{AllowAnonymous: scenario != "denied"}), commands.WithValidator[CreateAuthor](validation.ValidatorFunc[CreateAuthor](func(context.Context, CreateAuthor) ([]validation.Result, error) {
				if scenario == "validation" {
					return []validation.Result{{Severity: validation.Error, Message: "invalid child"}}, nil
				}
				return nil, nil
			}))))
			lateFailure := errors.New("failed after commit")
			commitAttempts := 0
			require(t, commands.Register[CreatePair](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ CreatePair) (bool, error) {
				aggregate, err := factory.Get(ctx, inv)
				require(t, err)
				require(t, aggregate.Apply(ctx, AuthorCreated{Name: "parent"}))
				if scenario == "lookup" {
					_, _ = inv.Pipeline().Execute(ctx, struct{ Unknown string }{})
				} else if scenario != "late failure" {
					child, err := inv.Pipeline().Execute(ctx, CreateAuthor{ID: "child", Name: "child"})
					if scenario == "nested success" {
						require(t, err)
						if !child.IsSuccess() {
							t.Fatal(child)
						}
						return true, nil
					}
				}
				commitAttempts++
				commit, err := aggregate.Commit(ctx)
				if scenario == "late failure" {
					require(t, err)
					if commit.Report.Disposition != commands.Committed {
						t.Fatal(commit)
					}
					return false, lateFailure
				}
				if !errors.Is(err, commands.ErrExecutionFailed) {
					t.Fatal("recorded failure did not prevent early commit", err)
				}
				return true, nil // Ignore both the nested outcome and the commit error.
			})))
			app, err := builder.Build()
			require(t, err)
			require(t, app.Start(ctx))
			t.Cleanup(func() { require(t, app.Shutdown(context.Background())) })
			result, err := app.Commands().Execute(ctx, CreatePair{ID: "parent"})
			parent := history(t, ctx, client, store, "Default", "parent")
			child := history(t, ctx, client, store, "Default", "child")
			if scenario == "nested success" {
				require(t, err)
				if !result.IsSuccess() || result.Completion().Disposition != commands.Committed || commitAttempts != 0 || len(parent) != 1 || len(child) != 1 {
					t.Fatal(result, parent, child, commitAttempts)
				}
				return
			}
			_, responsePresent := result.Response()
			if result.IsSuccess() || responsePresent || commitAttempts != 1 || len(child) != 0 {
				t.Fatal(result, err, parent, child, commitAttempts)
			}
			if scenario == "late failure" {
				if !errors.Is(err, lateFailure) || result.Completion().Disposition != commands.Committed || len(parent) != 1 {
					t.Fatal(result, err, parent)
				}
			} else if !errors.Is(err, commands.ErrExecutionFailed) || result.Completion().Disposition == commands.Committed || len(parent) != 0 {
				t.Fatal("failed command persisted", result, err, parent)
			}
		})
	}
}
