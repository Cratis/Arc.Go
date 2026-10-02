// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
)

func TestRenderBeforeSequentialPerItemInterception(t *testing.T) {
	var r queries.Registry
	events := []string{}
	items := []Item{{ID: 1, Name: "secret"}, {ID: 2, Name: "secret"}}
	mustRegister(t, queries.Register[Item](&r, "All", queries.Function(func(context.Context, queries.NoArguments) (providerQuery, error) {
		events = append(events, "perform")
		return providerQuery{}, nil
	}), public[queries.NoArguments]()))
	mustRegister(t, queries.RegisterRenderer(&r, func(context.Context, *execution.Scope) (queries.Renderer[providerQuery, []Item], error) {
		events = append(events, "renderer factory")
		return queries.RendererFunc[providerQuery, []Item](func(context.Context, providerQuery, queries.QueryContext) (queries.RendererResult[[]Item], error) {
			events = append(events, "render")
			return queries.RendererResult[[]Item]{Data: items, TotalItems: 22}, nil
		}), nil
	}))
	for _, name := range []string{"mask", "decorate"} {
		mustRegister(t, queries.RegisterReadModelInterceptor(&r, name, func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[Item], error) {
			events = append(events, name+" factory")
			return queries.InterceptorFunc[Item](func(ctx context.Context, item Item) (Item, error) {
				c, ok := queries.ContextFrom(ctx)
				if !ok || c.TotalItems() != 22 {
					t.Fatal("render total not installed")
				}
				events = append(events, name+string(rune('0'+item.ID)))
				if name == "mask" {
					item.Name = "safe"
				} else {
					item.Name += "!"
				}
				return item, nil
			}), nil
		}))
	}
	p := build(t, &r, queries.PipelineOptions{})
	result, err := queries.Perform[[]Item](context.Background(), p, "Item.All", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := result.Data()
	if data[0].Name != "safe!" || data[1].Name != "safe!" || items[0].Name != "secret" || !reflect.DeepEqual(events, []string{"perform", "renderer factory", "render", "mask factory", "decorate factory", "mask1", "decorate1", "mask2", "decorate2"}) {
		t.Fatalf("data=%+v events=%v", data, events)
	}
}
func TestReadyNullSkipsSingleInterceptionAndPointerValueTypesAreExact(t *testing.T) {
	var r queries.Registry
	valueCalls, pointerCalls := 0, 0
	mustRegister(t, queries.Register[Item](&r, "Null", queries.Function(func(context.Context, queries.NoArguments) (*Item, error) { return nil, nil }), public[queries.NoArguments]()))
	mustRegister(t, queries.Register[Item](&r, "Pointer", queries.Function(func(context.Context, queries.NoArguments) ([]*Item, error) {
		return []*Item{nil, {Name: "secret"}}, nil
	}), public[queries.NoArguments]()))
	mustRegister(t, queries.RegisterReadModelInterceptor(&r, "value", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[Item], error) {
		valueCalls++
		return queries.InterceptorFunc[Item](func(context.Context, Item) (Item, error) { return Item{}, nil }), nil
	}))
	mustRegister(t, queries.RegisterReadModelInterceptor(&r, "pointer", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[*Item], error) {
		pointerCalls++
		return queries.InterceptorFunc[*Item](func(_ context.Context, item *Item) (*Item, error) {
			copy := *item
			copy.Name = "safe"
			return &copy, nil
		}), nil
	}))
	p := build(t, &r, queries.PipelineOptions{})
	result, err := queries.Perform[*Item](context.Background(), p, "Item.Null", queries.Request{})
	data, present := result.Data()
	if err != nil || !result.IsSuccess() || !result.IsReady() || !present || data != nil || pointerCalls != 0 || valueCalls != 0 {
		t.Fatalf("ready-null=%+v, %v", result.Details(), err)
	}
	list, err := queries.Perform[[]*Item](context.Background(), p, "Item.Pointer", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := list.Data()
	if items[0] != nil || items[1].Name != "safe" || pointerCalls != 1 || valueCalls != 0 {
		t.Fatal("pointer interception not exact")
	}
}
func TestInterceptorFailureRetractsAllData(t *testing.T) {
	var r queries.Registry
	failure := errors.New("private masking failure")
	mustRegister(t, queries.Register[Item](&r, "All", queries.Function(func(context.Context, queries.NoArguments) ([]Item, error) { return []Item{{ID: 1}, {ID: 2}}, nil }), public[queries.NoArguments]()))
	mustRegister(t, queries.RegisterReadModelInterceptor(&r, "fail", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[Item], error) {
		return queries.InterceptorFunc[Item](func(_ context.Context, item Item) (Item, error) {
			if item.ID == 2 {
				return Item{}, failure
			}
			return item, nil
		}), nil
	}))
	p := build(t, &r, queries.PipelineOptions{})
	result, err := p.Perform(context.Background(), "Item.All", queries.Request{})
	if !errors.Is(err, failure) || !result.HasExceptions() {
		t.Fatal(err)
	}
	if _, present := result.Data(); present {
		t.Fatal("partially intercepted data published")
	}
}
