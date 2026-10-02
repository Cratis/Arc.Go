// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

type diResources struct {
	resource
	resolutions int
	value       string
}

func (r *diResources) Resolve(context.Context, di.Key) (any, error) {
	r.resolutions++
	return r.value, nil
}

type diFactory struct {
	scope *diResources
	opens int
}

func (f *diFactory) NewScope(context.Context) (di.Scope, error) { f.opens++; return f.scope, nil }
func (f *diFactory) Contains(di.Key) bool                       { return true }
func (f *diFactory) Owns(scope di.Scope) bool                   { return scope == f.scope }
func TestGeneratedPreparationSeamWithDIFakeAndStageSuppression(t *testing.T) {
	var r commands.Registry
	preparations, handlings := 0, 0
	must(t, commands.Register[Clear](&r, commands.Prepare(func(ctx context.Context, inv *commands.Invocation, _ Clear) (commands.Preparation[string], error) {
		preparations++
		value, err := execution.Resolve[string](ctx, inv.Scope())
		if err != nil {
			return commands.Preparation[string]{}, err
		}
		return commands.Provided(value), nil
	}, func(ctx context.Context, inv *commands.Invocation, _ Clear, provided string) (string, error) {
		handlings++
		value, err := execution.Resolve[string](ctx, inv.Scope())
		return provided + value, err
	}), commands.WithPreparationDependencies[Clear](di.KeyFor[string]()), commands.WithHandlingDependencies[Clear](di.KeyFor[string]())))
	resources := &diResources{value: "value"}
	factory := &diFactory{scope: resources}
	p := build(t, &r, commands.PipelineOptions{ScopeFactory: factory})
	if factory.opens != 0 || resources.resolutions != 0 {
		t.Fatal("Build activated DI")
	}
	validated, err := p.Validate(t.Context(), Clear{})
	must(t, err)
	if !validated.IsSuccess() || preparations != 0 || handlings != 0 || resources.resolutions != 0 {
		t.Fatal("Validate resolved stage dependencies")
	}
	result, err := commands.Execute[string](t.Context(), p, Clear{})
	must(t, err)
	if response, ok := result.Response(); !result.IsSuccess() || !ok || response != "valuevalue" || preparations != 1 || handlings != 1 || resources.resolutions != 2 {
		t.Fatal(result.Details(), resources.resolutions)
	}
	foreign, err := execution.BorrowScope(t.Context(), &diResources{})
	must(t, err)
	_, err = p.ExecuteScoped(t.Context(), foreign, Clear{})
	if !errors.Is(err, execution.ErrScopeOwner) {
		t.Fatal("foreign provider accepted", err)
	}
	must(t, foreign.Close(t.Context()))
}
func TestScopedAdapterPreservesSeparateProvideAndHandle(t *testing.T) {
	var r commands.Registry
	stages := []string{}
	must(t, commands.Register[Clear](&r, commands.Scoped(func(context.Context, *resource) (commands.Handler[Clear, int], error) {
		stages = append(stages, "factory")
		return commands.WithProvide(func(Clear, context.Context) (int, error) { stages = append(stages, "provide"); return 42, nil }, func(_ Clear, _ context.Context, value int) (int, error) {
			stages = append(stages, "handle")
			return value, nil
		}), nil
	})))
	p := build(t, &r, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return &resource{}, nil }})
	_, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	if len(stages) != 3 || stages[0] != "factory" || stages[1] != "provide" || stages[2] != "handle" {
		t.Fatal(stages)
	}
}
