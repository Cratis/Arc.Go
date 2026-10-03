// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type commandModel struct{ Name string }
type keyedCommand struct {
	ID string `arc:"key"`
}

func TestReadModelProviderPrecedenceIsIndependentOfOrder(t *testing.T) {
	for _, order := range [][]commands.ReadModelOwnership{
		{commands.FallbackReadModel, commands.DeclaredReadModel},
		{commands.DeclaredReadModel, commands.FallbackReadModel},
		{commands.OverrideReadModel, commands.FallbackReadModel, commands.DeclaredReadModel},
		{commands.DeclaredReadModel, commands.FallbackReadModel, commands.OverrideReadModel},
	} {
		var r commands.Registry
		want := commands.DeclaredReadModel
		if len(order) == 3 {
			want = commands.OverrideReadModel
		}
		calls := 0
		for _, owner := range order {
			must(t, commands.RegisterReadModelProvider[commandModel](&r, fmt.Sprint(owner), owner, func(_ context.Context, _ *commands.Invocation, key string) (commands.ReadModelInstance[commandModel], error) {
				calls++
				if owner != want || key != " key " {
					t.Fatal("wrong owner or changed key", owner, key)
				}
				return commands.ReadModelInstance[commandModel]{Exists: true}, nil
			}))
		}
		must(t, commands.Register[keyedCommand](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ keyedCommand) (commandModel, error) {
			return commands.RequireReadModel[commandModel](ctx, inv)
		})))
		p := build(t, &r, commands.PipelineOptions{})
		if calls != 0 {
			t.Fatal("Build activated provider")
		}
		result, err := p.Execute(t.Context(), keyedCommand{ID: " key "})
		must(t, err)
		if !result.IsSuccess() || calls != 1 {
			t.Fatal(result, calls)
		}
	}
}

func TestReadModelDuplicateDeclaredOwnersFailEvenWithOverride(t *testing.T) {
	var r commands.Registry
	resolve := func(context.Context, *commands.Invocation, string) (commands.ReadModelInstance[commandModel], error) {
		return commands.ReadModelInstance[commandModel]{}, nil
	}
	must(t, commands.RegisterReadModelProvider(&r, "first", commands.DeclaredReadModel, resolve))
	must(t, commands.RegisterReadModelProvider(&r, "application", commands.OverrideReadModel, resolve))
	if err := commands.RegisterReadModelProvider(&r, "second", commands.DeclaredReadModel, resolve); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
	_ = build(t, &r, commands.PipelineOptions{})
	if err := commands.RegisterReadModelProvider(&r, "fallback", commands.FallbackReadModel, resolve); !errors.Is(err, commands.ErrFrozen) {
		t.Fatal(err)
	}
}

func TestReadModelPresenceKeyAndFailureSemantics(t *testing.T) {
	cause := errors.New("reader or release failed")
	for _, tc := range []struct {
		name, key        string
		exists, required bool
		readerErr        error
		reason           validation.Reason
	}{
		{name: "present zero", key: "id", exists: true, required: true},
		{name: "optional absent", key: "id"},
		{name: "required absent", key: "id", required: true, reason: validation.DependencyUnavailable},
		{name: "missing optional key", reason: validation.MalformedRequest},
		{name: "blank optional key", key: "  ", reason: validation.MalformedRequest},
		{name: "reader failure", key: "id", readerErr: cause},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r commands.Registry
			calls := 0
			must(t, commands.RegisterReadModelProvider[commandModel](&r, "store", commands.DeclaredReadModel, func(context.Context, *commands.Invocation, string) (commands.ReadModelInstance[commandModel], error) {
				calls++
				return commands.ReadModelInstance[commandModel]{Exists: tc.exists}, tc.readerErr
			}))
			must(t, commands.Register[keyedCommand](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ keyedCommand) (commands.NoResponse, error) {
				if tc.required {
					_, err := commands.RequireReadModel[commandModel](ctx, inv)
					return commands.NoResponse{}, err
				}
				value, err := commands.ReadModelOrNil[commandModel](ctx, inv)
				if err == nil && (value != nil) != tc.exists {
					t.Fatal(value, tc.exists)
				}
				return commands.NoResponse{}, err
			})))
			result, err := build(t, &r, commands.PipelineOptions{}).Execute(t.Context(), keyedCommand{ID: tc.key})
			if tc.readerErr != nil {
				if !errors.Is(err, cause) || !result.HasExceptions() {
					t.Fatal(result, err)
				}
			} else if tc.reason != "" {
				if !errors.Is(err, validation.ErrRejected) || len(result.Details().ValidationResults) != 1 || result.Details().ValidationResults[0].Reason != tc.reason {
					t.Fatal(result, err)
				}
			} else {
				must(t, err)
				if !result.IsSuccess() {
					t.Fatal(result)
				}
			}
			if tc.reason == validation.MalformedRequest && calls != 0 {
				t.Fatal("invalid key invoked provider")
			}
		})
	}
}

func TestReadModelProviderCanCacheAcrossStagesButNotChildren(t *testing.T) {
	var r commands.Registry
	key := commands.NewStateKey[commands.ReadModelInstance[commandModel]]()
	reads := 0
	must(t, commands.RegisterReadModelProvider[commandModel](&r, "store", commands.DeclaredReadModel, func(ctx context.Context, inv *commands.Invocation, _ string) (commands.ReadModelInstance[commandModel], error) {
		cached, found, err := commands.FrameState(ctx, inv, key)
		if err != nil || found {
			return cached, err
		}
		reads++
		cached = commands.ReadModelInstance[commandModel]{Value: commandModel{Name: "loaded"}, Exists: true}
		return cached, commands.SetFrameState(ctx, inv, key, cached)
	}))
	must(t, commands.Register[keyedCommand](&r, commands.Prepare(func(ctx context.Context, inv *commands.Invocation, _ keyedCommand) (commands.Preparation[int], error) {
		_, err := commands.RequireReadModel[commandModel](ctx, inv)
		return commands.Provided(0), err
	}, func(ctx context.Context, inv *commands.Invocation, c keyedCommand, _ int) (commands.NoResponse, error) {
		_, err := commands.RequireReadModel[commandModel](ctx, inv)
		if err == nil && c.ID == "parent" {
			_, err = inv.Pipeline().Execute(ctx, keyedCommand{ID: "child"})
		}
		return commands.NoResponse{}, err
	})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), keyedCommand{ID: "parent"})
	must(t, err)
	if !result.IsSuccess() || reads != 2 {
		t.Fatal(result, reads)
	}
}

func TestValidationCanReadModelsWithoutStartingCompletion(t *testing.T) {
	var r commands.Registry
	reads := 0
	must(t, commands.RegisterReadModelProvider[commandModel](&r, "store", commands.DeclaredReadModel, func(context.Context, *commands.Invocation, string) (commands.ReadModelInstance[commandModel], error) {
		reads++
		return commands.ReadModelInstance[commandModel]{Exists: true}, nil
	}))
	must(t, commands.Register[keyedCommand](&r, commands.Void(func(keyedCommand, context.Context) error { t.Fatal("Handle ran"); return nil })))
	must(t, r.AddFilter("validator-read", func(context.Context, *execution.Scope) (commands.Filter, error) {
		return commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			_, err := commands.RequireReadModel[commandModel](ctx, inv)
			if reportErr := commands.ReportCommit(ctx, inv, commands.CompletionReport{Disposition: commands.Committed}); !errors.Is(reportErr, commands.ErrExecutionMismatch) {
				t.Fatal(reportErr)
			}
			return commands.Success(inv.CommandContext().CorrelationID()), err
		}), nil
	}))
	must(t, r.AddDeferredCommitParticipant("store", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
		t.Fatal("Validate activated terminal")
		return nil, nil
	}))
	result, err := build(t, &r, commands.PipelineOptions{}).Validate(t.Context(), keyedCommand{ID: "id"})
	must(t, err)
	if !result.IsSuccess() || reads != 1 {
		t.Fatal(result, reads)
	}
}

func ExampleRegisterReadModelProvider() {
	var registry commands.Registry
	if err := commands.RegisterReadModelProvider[commandModel](&registry, "authors", commands.DeclaredReadModel,
		func(ctx context.Context, inv *commands.Invocation, key string) (commands.ReadModelInstance[commandModel], error) {
			return commands.ReadModelInstance[commandModel]{Value: commandModel{Name: "Ada"}, Exists: key == "ada"}, nil
		}); err != nil {
		panic(err)
	}
	if err := commands.Register[keyedCommand](&registry, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ keyedCommand) (string, error) {
		model, err := commands.RequireReadModel[commandModel](ctx, inv)
		return model.Name, err
	})); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := commands.Execute[string](context.Background(), pipeline, keyedCommand{ID: "ada"})
	if err != nil {
		panic(err)
	}
	name, _ := result.Response()
	fmt.Println(name)
	// Output: Ada
}
