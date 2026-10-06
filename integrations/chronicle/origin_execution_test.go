// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/chronicle.go/eventsequences"
)

type OriginCommand struct {
	ID          c.EventSourceID
	Depth       int
	Independent bool
}

func originApp(t *testing.T, factory *notifyingFactory) (*arc.Builder, *c.Integration) {
	t.Helper()
	builder, err := arc.NewBuilder(arc.Options{})
	must(t, err)
	integration, err := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}, nil
	}, Transactions: factory, Events: catalog{}, Appends: factory})
	must(t, err)
	must(t, integration.Install(builder))
	return builder, integration
}

func resolvedOrigin(t *testing.T, ctx context.Context) eventsequences.Origin {
	t.Helper()
	origin, handled, err := sdk.ResolveAppendOrigin(ctx)
	if err != nil || !handled || origin == (eventsequences.Origin{}) {
		t.Fatalf("executing origin = %v, %v, %v", origin, handled, err)
	}
	return origin
}

func TestOriginsJoinChildrenButIndependentRootsOverrideInheritedOrigin(t *testing.T) {
	factory := &notifyingFactory{}
	builder, _ := originApp(t, factory)
	var app *arc.Application
	var roots []eventsequences.Origin
	validations := 0
	preauthorized := 0
	must(t, builder.Commands().AddAuthorizationFilter("observe-before-publication", func(context.Context, *execution.Scope) (commands.AuthorizationFilter, error) {
		return commands.AsAuthorizationFilter(commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			origin, handled, err := sdk.ResolveAppendOrigin(ctx)
			if err != nil || !handled || origin != (eventsequences.Origin{}) {
				t.Errorf("Arc snapshot without published token = %v, %v, %v", origin, handled, err)
			}
			preauthorized++
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		})), nil
	}))
	must(t, builder.Commands().AddFilter("observe-validation", func(context.Context, *execution.Scope) (commands.Filter, error) {
		return commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			if inv.CommandContext().IsValidationOnly() {
				origin, handled, err := sdk.ResolveAppendOrigin(ctx)
				if err != nil || !handled || origin != (eventsequences.Origin{}) {
					t.Errorf("validation origin = %v, %v, %v", origin, handled, err)
				}
				validations++
			}
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}), nil
	}))
	must(t, commands.Register[OriginCommand](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, command OriginCommand) (commands.NoResponse, error) {
		origin := resolvedOrigin(t, ctx)
		if command.Depth == 0 {
			roots = append(roots, origin)
		}
		if command.Depth < 2 {
			child := OriginCommand{ID: "child", Depth: command.Depth + 1}
			result, err := inv.Pipeline().Execute(ctx, child)
			if err != nil || !result.IsSuccess() {
				return commands.NoResponse{}, errors.New("joined child failed")
			}
			if got := resolvedOrigin(t, ctx); got != origin {
				t.Error("parent token changed after child")
			}
			validated, err := inv.Pipeline().Validate(eventsequences.WithOrigin(ctx, origin), child)
			if err != nil || !validated.IsSuccess() {
				return commands.NoResponse{}, errors.New("nested validation failed")
			}
		}
		if command.Depth > 0 {
			// The root token is shared even though each child has a new snapshot.
			if origin != roots[len(roots)-1] {
				t.Error("joined child or grandchild origin differs")
			}
		}
		if command.Independent {
			result, err := app.Commands().Execute(ctx, OriginCommand{ID: "independent"})
			if err != nil || !result.IsSuccess() {
				return commands.NoResponse{}, errors.New("independent root failed")
			}
		}
		return commands.NoResponse{}, nil
	})))
	app = start(t, builder)
	inherited := eventsequences.NewOrigin()
	ctx := eventsequences.WithOrigin(correlation.WithID(t.Context(), correlation.ID{1}), inherited)
	result, err := app.Commands().Validate(ctx, OriginCommand{ID: "validation"})
	must(t, err)
	if !result.IsSuccess() || len(roots) != 0 || len(factory.subscriptions) != 0 || factory.origins != 0 {
		t.Fatal("validation executed or subscribed")
	}
	executed, err := app.Commands().Execute(ctx, OriginCommand{ID: "root", Independent: true})
	must(t, err)
	if !executed.IsSuccess() || executed.Completion().Disposition != commands.NoPersistedWork || len(roots) != 2 || roots[0] == roots[1] || roots[0] == inherited || roots[1] == inherited || validations != 5 || preauthorized != 11 || factory.origins != 2 {
		t.Fatal(executed, roots, validations)
	}
	if origin, handled, err := sdk.ResolveAppendOrigin(ctx); err != nil || handled || origin != (eventsequences.Origin{}) {
		t.Fatal("non-Arc resolver must defer to SDK fallback", origin, handled, err)
	}
}

func TestDeniedExecutionNeverPublishesOriginOrSubscribes(t *testing.T) {
	factory := &notifyingFactory{}
	builder, _ := originApp(t, factory)
	must(t, builder.Commands().AddAuthorizationFilter("deny", func(context.Context, *execution.Scope) (commands.AuthorizationFilter, error) {
		return commands.AsAuthorizationFilter(commands.FilterFunc(func(ctx context.Context, _ *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			origin, handled, err := sdk.ResolveAppendOrigin(ctx)
			if err != nil || !handled || origin != (eventsequences.Origin{}) {
				t.Error(origin, handled, err)
			}
			return commands.Result[commands.NoResponse]{}, nil
		})), nil
	}))
	must(t, commands.Register[OriginCommand](builder, commands.Handle(func(OriginCommand, context.Context) (commands.NoResponse, error) {
		t.Error("denied handler executed")
		return commands.NoResponse{}, nil
	})))
	app := start(t, builder)
	result, _ := app.Commands().Execute(eventsequences.WithOrigin(t.Context(), eventsequences.NewOrigin()), OriginCommand{ID: "denied"})
	if result.IsSuccess() || len(factory.subscriptions) != 0 {
		t.Fatal(result, factory.subscriptions)
	}
}

func TestConcurrentSameCorrelationImmediateRejectionOnlyPoisonsItsOwner(t *testing.T) {
	for _, rejectFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "rejected first", false: "accepted first"}[rejectFirst], func(t *testing.T) {
			factory := &notifyingFactory{}
			builder, _ := originApp(t, factory)
			entered := make(chan eventsequences.Origin, 2)
			firstDone := make(chan struct{})
			bothEntered := make(chan struct{})
			must(t, commands.Register[SharedCorrelationCommand](builder, commands.Handle(func(command SharedCorrelationCommand, ctx context.Context) (commands.NoResponse, error) {
				origin := resolvedOrigin(t, ctx)
				entered <- origin
				wait := firstDone
				if command.First {
					wait = bothEntered
				}
				select {
				case <-wait:
				case <-ctx.Done():
					return commands.NoResponse{}, ctx.Err()
				}
				disposition := commands.Committed
				var failure error
				if command.Reject {
					disposition, failure = commands.NotCommitted, c.ErrInvalid
				}
				factory.notify(origin, c.CommitResult{Report: commands.CompletionReport{Disposition: disposition}}, failure)
				return commands.NoResponse{}, nil
			})))
			app := start(t, builder)
			inherited := eventsequences.NewOrigin()
			ctx := eventsequences.WithOrigin(correlation.WithID(t.Context(), correlation.ID{1}), inherited)
			type outcome struct {
				result commands.Result[any]
				err    error
				reject bool
			}
			results := make(chan outcome, 2)
			go func() {
				result, err := app.Commands().Execute(ctx, SharedCorrelationCommand{ID: "first", First: true, Reject: rejectFirst})
				results <- outcome{result, err, rejectFirst}
				close(firstDone)
			}()
			go func() {
				result, err := app.Commands().Execute(ctx, SharedCorrelationCommand{ID: "second", Reject: !rejectFirst})
				results <- outcome{result, err, !rejectFirst}
			}()
			first, second := <-entered, <-entered
			if first == second || first == inherited || second == inherited {
				t.Error("independent roots reused origin")
			}
			close(bothEntered)
			for range 2 {
				got := <-results
				if got.reject {
					if got.result.IsSuccess() || !errors.Is(got.err, c.ErrInvalid) || got.result.Completion().Disposition != commands.NotCommitted {
						t.Error(got)
					}
				} else if !got.result.IsSuccess() || got.err != nil || got.result.Completion().Disposition != commands.Committed {
					t.Error(got)
				}
			}
		})
	}
}
