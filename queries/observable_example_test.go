// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"fmt"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func ExampleRegisterObservable() {
	ctx := context.Background()
	state, err := observable.NewState(Item{Name: "Ada"}, observable.SubjectOptions[Item]{})
	if err != nil {
		panic(err)
	}
	var registry queries.Registry
	err = queries.RegisterObservable[Item](&registry, "Current", queries.Function(
		func(context.Context, queries.NoArguments) (observable.Source[Item], error) { return state, nil },
	), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}))
	if err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(queries.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := queries.Perform[Item](ctx, pipeline, "Item.Current", queries.Request{})
	if err != nil {
		panic(err)
	}
	item, present := result.Data()
	fmt.Println(result.IsReady(), present, item.Name)
	if err := pipeline.(queries.ObservablePipeline).CloseObservations(ctx); err != nil {
		panic(err)
	}
	// Output: true true Ada
}
