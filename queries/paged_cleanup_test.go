// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
)

type pagedSelection struct{}

func TestSuccessfulPagedRendererThenFailedCloseRetractsCountAndData(t *testing.T) {
	var registry queries.Registry
	failure := errors.New("private cleanup failure")
	holder := &plainResources{closeErr: failure}
	rendered := false
	mustRegister(t, queries.RegisterRenderer[pagedSelection, []Item](&registry, func(context.Context, *execution.Scope) (queries.Renderer[pagedSelection, []Item], error) {
		return queries.RendererFunc[pagedSelection, []Item](func(_ context.Context, _ pagedSelection, c queries.QueryContext) (queries.RendererResult[[]Item], error) {
			rendered = true
			if c.Parameters().Paging.Page != 4 {
				t.Fatal("lost requested page")
			}
			return queries.RendererResult[[]Item]{Data: []Item{{ID: 1, Name: "sensitive"}}, TotalItems: 51}, nil
		}), nil
	}))
	mustRegister(t, queries.Register[Item](&registry, "Paged", queries.Function(func(context.Context, queries.NoArguments) (pagedSelection, error) { return pagedSelection{}, nil }), public[queries.NoArguments]()))
	pipeline := build(t, &registry, queries.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
	result, err := queries.Perform[[]Item](t.Context(), pipeline, "Item.Paged", queries.RequestFor(queries.NoArguments{}, queries.Parameters{Paging: queries.Paging{IsPaged: true, Page: 4, Size: 10}}))
	if !errors.Is(err, failure) || !rendered || holder.closes != 1 || result.IsSuccess() {
		t.Fatalf("result %v err %v rendered %v closes %d", result.Details(), err, rendered, holder.closes)
	}
	if _, present := result.Data(); present || result.Details().Paging != (queries.PagingInfo{}) || result.Details().ChangeSet != nil {
		t.Fatal("cleanup failure leaked data/count/changes", result.Details())
	}
	wire, err := result.MarshalJSON()
	mustRegister(t, err)
	var envelope map[string]json.RawMessage
	mustRegister(t, json.Unmarshal(wire, &envelope))
	if string(envelope["paging"]) != `{"page":0,"size":0,"totalItems":0,"totalPages":0}` || envelope["data"] != nil || envelope["changeSet"] != nil {
		t.Fatalf("failure envelope %s", wire)
	}
}
