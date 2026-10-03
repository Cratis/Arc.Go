// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func ExampleObservedCollection() {
	state, err := observable.NewState(queries.ObservedCollection[Item]{Items: []Item{{Name: "Ada"}}, Version: 1, Generation: "tasks"}, observable.SubjectOptions[queries.ObservedCollection[Item]]{})
	if err != nil {
		panic(err)
	}
	var registry queries.Registry
	err = queries.RegisterObservable[Item](&registry, "All", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[queries.ObservedCollection[Item]], error) {
		return state, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}), queries.WithCollectionIdentity[queries.NoArguments](func(item Item) (any, error) { return item.Name, nil }))
	if err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(queries.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := queries.Perform[[]Item](context.Background(), pipeline, "Item.All", queries.Request{})
	if err != nil {
		panic(err)
	}
	items, present := result.Data()
	fmt.Println(present, items[0].Name)
	// Output: true Ada
}
func TestObservedCollectionPointerDeclarationFailsWithoutCallingNilWrapper(t *testing.T) {
	var r queries.Registry
	err := queries.RegisterObservable[Item](&r, "All", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[*queries.ObservedCollection[Item]], error) {
		t.Fatal("registration activated performer")
		return nil, nil
	}), public[queries.NoArguments]())
	if !errors.Is(err, queries.ErrResponseType) {
		t.Fatalf("pointer wrapper error = %v", err)
	}
}

func TestObservedCollectionDeclarationUnwrapsItemsAndRejectsWrongIdentity(t *testing.T) {
	state, err := observable.NewState(queries.ObservedCollection[Item]{Items: []Item{}, Version: 1}, observable.SubjectOptions[queries.ObservedCollection[Item]]{})
	mustRegister(t, err)
	p := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{})
	q, _ := p.Lookup("Item.Observe")
	if q.DataType() != reflect.TypeFor[[]Item]() || q.EmissionType() != reflect.TypeFor[queries.ObservedCollection[Item]]() {
		t.Fatal("wrapper metadata leaked into client data")
	}
	result, err := queries.Perform[[]Item](context.Background(), p, "Item.Observe", queries.Request{})
	items, present := result.Data()
	if err != nil || !present || items == nil || len(items) != 0 {
		t.Fatalf("empty items = %v, %v, %v", items, present, err)
	}
	r := observableRegistry(t, state, queries.WithCollectionIdentity[queries.NoArguments](func(string) (any, error) { return "id", nil }))
	if _, err := r.Build(queries.PipelineOptions{}); err == nil {
		t.Fatal("wrong typed identity accepted")
	}
	mustRegister(t, p.CloseObservations(context.Background()))
}
