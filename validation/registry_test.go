// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

func TestGraphRegistryRejectsDuplicatesAndFreezesWithoutFactories(t *testing.T) {
	var registry validation.Registry
	calls := 0
	factory := func(context.Context, *execution.Scope) (validation.Validator[string], error) {
		calls++
		return validation.ValidatorFunc[string](func(context.Context, string) ([]validation.Result, error) { return nil, nil }), nil
	}
	if err := validation.RegisterScoped(&registry, factory); err != nil {
		t.Fatal(err)
	}
	if err := validation.RegisterScopedConcept(&registry, factory); !errors.Is(err, validation.ErrDuplicate) {
		t.Fatal(err)
	}
	graph, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("Build invoked factory")
	}
	if err := validation.RegisterScoped(&registry, factory); !errors.Is(err, validation.ErrFrozen) {
		t.Fatal(err)
	}
	if _, err := graph.Validate(t.Context(), nil, "input"); !errors.Is(err, execution.ErrInvalidScope) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("factory invoked without scope")
	}
	scope, err := execution.OpenScope(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Validate(t.Context(), scope, "input"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if err := scope.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestScopedValidatorViewExpiresAndFactoryFailuresStayInfrastructure(t *testing.T) {
	failure := errors.New("missing service")
	for _, tc := range []struct {
		name    string
		factory validation.Factory[validation.Validator[int]]
		want    error
	}{
		{"failure", func(context.Context, *execution.Scope) (validation.Validator[int], error) { return nil, failure }, failure},
		{"nil", func(context.Context, *execution.Scope) (validation.Validator[int], error) {
			return validation.ValidatorFunc[int](nil), nil
		}, validation.ErrInvalidValidator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var registry validation.Registry
			if err := validation.RegisterScoped(&registry, tc.factory); err != nil {
				t.Fatal(err)
			}
			graph, err := registry.Build()
			if err != nil {
				t.Fatal(err)
			}
			scope, err := execution.OpenScope(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			results, err := graph.Validate(t.Context(), scope, 1)
			var validationFailure validation.Failure
			if len(results) != 0 || !errors.Is(err, tc.want) || errors.As(err, &validationFailure) {
				t.Fatalf("results/error = %v/%v", results, err)
			}
			if err := scope.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	var registry validation.Registry
	var view *execution.Scope
	if err := validation.RegisterScoped(&registry, func(_ context.Context, scope *execution.Scope) (validation.Validator[int], error) {
		view = scope
		return validation.ValidatorFunc[int](func(ctx context.Context, _ int) ([]validation.Result, error) {
			if !errors.Is(scope.Close(ctx), execution.ErrScopeView) {
				t.Error("validator owning scope")
			}
			return nil, scope.CheckContext(ctx)
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	graph, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := execution.OpenScope(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Validate(t.Context(), scope, 1); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(view.CheckContext(t.Context()), execution.ErrScopeExpired) {
		t.Fatal("retained factory scope is live")
	}
	if err := scope.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRejectsInvalidTagsWithoutRunningRules(t *testing.T) {
	type invalid struct {
		Name string `validate:"typo"`
	}
	var registry validation.Registry
	if err := validation.Register(&registry, validation.ValidatorFunc[invalid](func(context.Context, invalid) ([]validation.Result, error) { t.Fatal("rule called"); return nil, nil })); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Build(); !errors.Is(err, validation.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if err := validation.Register(&registry, validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) { return nil, nil })); err != nil {
		t.Fatal("failed Build froze builder", err)
	}
}
