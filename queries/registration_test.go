// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

type Item struct {
	ID   int    `json:"id" arc:"identity" chronicle:"key"`
	Name string `json:"name"`
}
type otherModel struct {
	Name string `json:"name"`
}
type taggedModel struct {
	_    struct{} `json:"-" arc:"readmodel,name=Stable,namespace=Shop,allow-anonymous"`
	Name string   `json:"name"`
}

func mustRegister(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func build(t *testing.T, r *queries.Registry, o queries.PipelineOptions) queries.Pipeline {
	t.Helper()
	p, err := r.Build(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func public[A any]() queries.Option[A] {
	return queries.WithAuthorization[A](metadata.Authorization{AllowAnonymous: true})
}
func itemPerformer() queries.Performer[queries.NoArguments, Item] {
	return queries.Function(func(context.Context, queries.NoArguments) (Item, error) { return Item{Name: "Ada"}, nil })
}

func TestReadModelOwnsMultipleQueriesAndFrozenSnapshots(t *testing.T) {
	r, err := queries.NewRegistry(queries.RegistryOptions{Namespace: "Inventory"})
	mustRegister(t, err)
	mustRegister(t, queries.RegisterReadModel[Item](r, queries.WithModelIdentity(metadata.TypeName{Namespace: "Shop", Name: "Product"}), queries.WithModelAuthorization(metadata.Authorization{AllowAnonymous: true})))
	mustRegister(t, queries.Register[Item](r, "All", queries.Function(func(context.Context, queries.NoArguments) ([]Item, error) { return nil, nil })))
	mustRegister(t, queries.Register[Item](r, "Current", itemPerformer()))
	catalog := r.Catalog()
	catalog.Queries[0].ReadModel.Name = "Mutated"
	catalog.Queries[0].ReadModelAuthorization.AllowAnonymous = false
	p := build(t, r, queries.PipelineOptions{})
	q, ok := p.Lookup("Shop.Product.All")
	if !ok || q.ReadModelType() != reflect.TypeFor[Item]() || q.DataType() != reflect.TypeFor[[]Item]() {
		t.Fatalf("registration = %+v, %v", q, ok)
	}
	descriptor := q.Descriptor()
	descriptor.ReadModelAuthorization.AllowAnonymous = false
	if !q.Descriptor().ReadModelAuthorization.AllowAnonymous {
		t.Fatal("descriptor alias")
	}
	if err := queries.Register[Item](r, "Later", itemPerformer()); !errors.Is(err, queries.ErrFrozen) {
		t.Fatal(err)
	}
	if _, err := r.Build(queries.PipelineOptions{}); !errors.Is(err, queries.ErrFrozen) {
		t.Fatal(err)
	}
}
func TestModelTagsAndExplicitMethodAuthorizationReplacement(t *testing.T) {
	var r queries.Registry
	mustRegister(t, queries.Register[taggedModel](&r, "Current", queries.Function(func(context.Context, queries.NoArguments) (*taggedModel, error) { return nil, nil }), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{})))
	q := r.Catalog().Queries[0]
	if q.Identity() != "Shop.Stable.Current" || q.ReadModelAuthorization == nil || !q.ReadModelAuthorization.AllowAnonymous || q.Authorization == nil || q.Authorization.AllowAnonymous {
		t.Fatalf("descriptor = %+v", q)
	}
	p := build(t, &r, queries.PipelineOptions{})
	result, err := p.Perform(context.Background(), "Shop.Stable.Current", queries.Request{})
	if err != nil || result.IsAuthorized() {
		t.Fatalf("result = %+v, %v", result.Details(), err)
	}
}
func TestExactOutputEligibility(t *testing.T) {
	tests := []struct {
		name     string
		register func(*queries.Registry) error
		want     error
	}{
		{"value", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", itemPerformer(), public[queries.NoArguments]())
		}, nil},
		{"pointer", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) (*Item, error) { return nil, nil }), public[queries.NoArguments]())
		}, nil},
		{"slice pointers", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) ([]*Item, error) { return nil, nil }), public[queries.NoArguments]())
		}, nil},
		{"array", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) ([2]Item, error) { return [2]Item{}, nil }), public[queries.NoArguments]())
		}, nil},
		{"page", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) (queries.Page[*Item], error) {
				return queries.Page[*Item]{}, nil
			}), public[queries.NoArguments]())
		}, nil},
		{"pointer page rejected without invoking nil method", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) (*queries.Page[Item], error) { return nil, nil }))
		}, queries.ErrResponseType},
		{"unrelated", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) (otherModel, error) { return otherModel{}, nil }), public[queries.NoArguments]())
		}, queries.ErrResponseType},
		{"channel", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) (<-chan Item, error) { return nil, nil }))
		}, queries.ErrUnsupportedObservable},
		{"iterator", func(r *queries.Registry) error {
			return queries.Register[Item](r, "One", queries.Function(func(context.Context, queries.NoArguments) (func(func(Item) bool), error) { return nil, nil }))
		}, queries.ErrUnsupportedObservable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var r queries.Registry
			err := tc.register(&r)
			if err == nil {
				_, err = r.Build(queries.PipelineOptions{})
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
	var r queries.Registry
	if err := queries.RegisterReadModel[*Item](&r); !errors.Is(err, queries.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if err := queries.Register[Item](&r, "Absent", queries.Performer[queries.NoArguments, Item]{}); !errors.Is(err, queries.ErrMissingPerformer) {
		t.Fatal(err)
	}
}
func TestDuplicateAndFailedBuildEditability(t *testing.T) {
	var r queries.Registry
	mustRegister(t, queries.Register[Item](&r, "One", itemPerformer(), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "Unknown"}}})))
	if err := queries.Register[Item](&r, "One", itemPerformer()); !errors.Is(err, queries.ErrDuplicate) {
		t.Fatal(err)
	}
	if _, err := r.Build(queries.PipelineOptions{}); err == nil {
		t.Fatal("unknown policy accepted")
	}
	mustRegister(t, queries.Register[Item](&r, "Two", itemPerformer(), public[queries.NoArguments]()))
	if err := queries.Register[Item](&r, "Three", itemPerformer(), queries.WithPath[queries.NoArguments]("/first"), queries.WithPath[queries.NoArguments]("/second")); !errors.Is(err, queries.ErrDuplicate) {
		t.Fatal(err)
	}
}
func TestDescriptorOverridesModelAndCatalogCompatibility(t *testing.T) {
	var r queries.Registry
	mustRegister(t, queries.RegisterReadModel[Item](&r, queries.WithModelIdentity(metadata.TypeName{Name: "Other"})))
	descriptor := metadata.Query{ReadModel: metadata.TypeName{Name: "Pinned"}, Name: "One", Authorization: &metadata.Authorization{AllowAnonymous: true}}
	mustRegister(t, queries.Register[Item](&r, "ignored", itemPerformer(), queries.WithDescriptor[queries.NoArguments](descriptor)))
	p := build(t, &r, queries.PipelineOptions{})
	if _, ok := p.Lookup("Pinned.One"); !ok {
		t.Fatal("descriptor identity overwritten")
	}
}
