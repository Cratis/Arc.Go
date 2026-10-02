// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"cmp"
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
)

func TestPagingSkipValidationAndDirectionGrammar(t *testing.T) {
	for _, tc := range []struct {
		page queries.PageNumber
		size queries.PageSize
		skip int32
	}{{2, 10, 20}, {-1, 10, 0}, {math.MaxInt32, math.MaxInt32, math.MaxInt32}, {math.MinInt32, math.MinInt32, math.MaxInt32}} {
		if got := (queries.Paging{Page: tc.page, Size: tc.size}).Skip(); got != tc.skip {
			t.Fatalf("Skip = %d, want %d", got, tc.skip)
		}
	}
	findings := (queries.Paging{Page: -1, Size: 0, IsPaged: true}).Validate()
	if len(findings) != 2 || findings[0].Message != "Page number must be greater than or equal to 0" || findings[0].Members[0] != "page" || findings[1].Message != "Page size must be greater than 0" || findings[1].Members[0] != "size" {
		t.Fatalf("findings = %+v", findings)
	}
	if len((queries.Paging{Page: -1, Size: -1}).Validate()) != 0 {
		t.Fatal("inactive paging validated")
	}
	for _, s := range []string{"asc", "ASCENDING", "desc", "Descending"} {
		if _, err := queries.ParseSortDirection(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{"", "unspecified", "down", " asc "} {
		if _, err := queries.ParseSortDirection(s); !errors.Is(err, queries.ErrInvalidSorting) {
			t.Fatal(err)
		}
	}
}
func TestSliceRendererOptInStableOrderingCountAndWindow(t *testing.T) {
	renderer, err := queries.NewSliceRenderer(queries.SortColumn[Item]{Field: "name", Compare: func(a, b Item) int { return cmp.Compare(a.Name, b.Name) }})
	mustRegister(t, err)
	items := []Item{{ID: 1, Name: "same"}, {ID: 2, Name: "Ada"}, {ID: 3, Name: "same"}}
	var r queries.Registry
	for _, name := range []string{"All", "Recent"} {
		mustRegister(t, queries.Register[Item](&r, name, queries.Function(func(context.Context, queries.NoArguments) ([]Item, error) { return items, nil }), public[queries.NoArguments]()))
	}
	factories := 0
	mustRegister(t, queries.RegisterRenderer(&r, func(context.Context, *execution.Scope) (queries.Renderer[[]Item, []Item], error) {
		factories++
		return renderer, nil
	}))
	p := build(t, &r, queries.PipelineOptions{})
	if factories != 0 {
		t.Fatal("Build activated renderer")
	}
	parameters := queries.Parameters{Paging: queries.Paging{Page: 1, Size: 1, IsPaged: true}, Sorting: queries.Sorting{Field: "Name", Direction: queries.Ascending}}
	result, err := queries.Perform[[]Item](context.Background(), p, "Item.All", queries.RequestFor(queries.NoArguments{}, parameters))
	if err != nil || !result.IsSuccess() {
		t.Fatalf("%+v, %v", result.Details(), err)
	}
	data, _ := result.Data()
	if len(data) != 1 || data[0].ID != 1 || result.Details().Paging.TotalItems != 3 || result.Details().Paging.TotalPages() != 3 || items[0].ID != 1 {
		t.Fatalf("data = %+v, paging = %+v", data, result.Details().Paging)
	}
	parameters.Paging.Page = 2
	result, err = queries.Perform[[]Item](context.Background(), p, "Item.Recent", queries.RequestFor(queries.NoArguments{}, parameters))
	if err != nil {
		t.Fatal(err)
	}
	data, _ = result.Data()
	if len(data) != 1 || data[0].ID != 3 || factories != 2 {
		t.Fatal("stable order or reuse failed")
	}
	parameters.Sorting.Field = "notAllowed"
	result, err = queries.Perform[[]Item](context.Background(), p, "Item.All", queries.RequestFor(queries.NoArguments{}, parameters))
	if !errors.Is(err, queries.ErrInvalidSorting) || result.IsValid() {
		t.Fatalf("%+v %v", result.Details(), err)
	}
	if _, err := queries.NewSliceRenderer(queries.SortColumn[Item]{Field: "unknown", Compare: func(Item, Item) int { return 0 }}); err == nil {
		t.Fatal("unknown wire column accepted")
	}
}
func TestPlainSliceRemainsUnpagedAndPageIsNotPagedTwice(t *testing.T) {
	var r queries.Registry
	items := []Item{{ID: 1}, {ID: 2}}
	mustRegister(t, queries.Register[Item](&r, "Plain", queries.Function(func(context.Context, queries.NoArguments) ([]Item, error) { return items, nil }), public[queries.NoArguments]()))
	mustRegister(t, queries.Register[Item](&r, "Page", queries.Function(func(context.Context, queries.NoArguments) (queries.Page[Item], error) {
		return queries.Page[Item]{Items: items, TotalItems: 9}, nil
	}), public[queries.NoArguments]()))
	p := build(t, &r, queries.PipelineOptions{})
	parameters := queries.Parameters{Paging: queries.Paging{Page: 5, Size: 1, IsPaged: true}}
	for _, name := range []queries.FullyQualifiedQueryName{"Item.Plain", "Item.Page"} {
		result, err := queries.Perform[[]Item](context.Background(), p, name, queries.RequestFor(queries.NoArguments{}, parameters))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := result.Data()
		if !reflect.DeepEqual(data, items) {
			t.Fatal("implicitly paged items")
		}
		paging := result.Details().Paging
		if name == "Item.Plain" && paging != (queries.PagingInfo{}) {
			t.Fatal("plain slice counted")
		}
		if name == "Item.Page" && paging.TotalItems != 9 {
			t.Fatal("page metadata lost")
		}
	}
}

type providerQuery struct{ Prefix string }

type nullableProvider interface{ ProviderName() string }

func (*providerQuery) ProviderName() string { return "provider" }

func TestNilProviderOutputSkipsRendererAndInterceptors(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		var r queries.Registry
		if pointer {
			mustRegister(t, queries.Register[Item](&r, "Nil", queries.Function(func(context.Context, queries.NoArguments) (*providerQuery, error) { return nil, nil })))
			mustRegister(t, queries.RegisterRenderer(&r, func(context.Context, *execution.Scope) (queries.Renderer[*providerQuery, []Item], error) {
				t.Fatal("nil pointer activated renderer")
				return nil, nil
			}))
		} else {
			mustRegister(t, queries.Register[Item](&r, "Nil", queries.Function(func(context.Context, queries.NoArguments) (nullableProvider, error) { return nil, nil })))
			mustRegister(t, queries.RegisterRenderer(&r, func(context.Context, *execution.Scope) (queries.Renderer[nullableProvider, []Item], error) {
				t.Fatal("nil interface activated renderer")
				return nil, nil
			}))
		}
		mustRegister(t, queries.RegisterReadModelInterceptor[Item](&r, "never", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[Item], error) {
			t.Fatal("ready-null activated interceptor")
			return nil, nil
		}))
		p := build(t, &r, queries.PipelineOptions{})
		result, err := queries.Perform[[]Item](t.Context(), p, "Item.Nil", queries.RequestFor(queries.NoArguments{}, queries.Parameters{Paging: queries.Paging{Size: 10, IsPaged: true}}))
		mustRegister(t, err)
		if data, present := result.Data(); !result.IsSuccess() || !result.IsReady() || !present || data != nil || result.Details().Paging.TotalItems != 0 {
			t.Fatal("nil provider is not ready-null", result.Details(), data, present)
		}
	}
}

func TestProviderRendererOwnershipReuseAndOverride(t *testing.T) {
	var r queries.Registry
	calls := 0
	factory := func(context.Context, *execution.Scope) (queries.Renderer[providerQuery, []Item], error) {
		calls++
		return queries.RendererFunc[providerQuery, []Item](func(_ context.Context, q providerQuery, _ queries.QueryContext) (queries.RendererResult[[]Item], error) {
			return queries.RendererResult[[]Item]{Data: []Item{{Name: q.Prefix}}, TotalItems: 12}, nil
		}), nil
	}
	mustRegister(t, queries.RegisterRenderer(&r, factory))
	performer := queries.Function(func(context.Context, queries.NoArguments) (providerQuery, error) {
		return providerQuery{Prefix: "reusable"}, nil
	})
	mustRegister(t, queries.Register[Item](&r, "All", performer, public[queries.NoArguments]()))
	override := func(context.Context, *execution.Scope) (queries.Renderer[providerQuery, *Item], error) {
		return queries.RendererFunc[providerQuery, *Item](func(context.Context, providerQuery, queries.QueryContext) (queries.RendererResult[*Item], error) {
			return queries.RendererResult[*Item]{Data: &Item{Name: "override"}}, nil
		}), nil
	}
	mustRegister(t, queries.Register[Item](&r, "One", performer, public[queries.NoArguments](), queries.WithRenderer[queries.NoArguments](override)))
	p := build(t, &r, queries.PipelineOptions{})
	if calls != 0 {
		t.Fatal("activated before perform")
	}
	one, err := queries.Perform[*Item](context.Background(), p, "Item.One", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := one.Data()
	if data.Name != "override" || calls != 0 {
		t.Fatal("override ignored")
	}
	all, err := queries.Perform[[]Item](context.Background(), p, "Item.All", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := all.Data()
	if list[0].Name != "reusable" || calls != 1 {
		t.Fatal("reusable renderer ignored")
	}
	if _, err := queries.Perform[*Item](context.Background(), p, "Item.All", queries.Request{}); !errors.Is(err, queries.ErrResponseType) || calls != 1 {
		t.Fatal("typed contract not checked before activation", err)
	}
	if err := queries.RegisterRenderer(&r, factory); !errors.Is(err, queries.ErrFrozen) {
		t.Fatal(err)
	}
}
