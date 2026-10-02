// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/queries"
)

type wrongDataPipeline struct{ queries.Pipeline }

func (p wrongDataPipeline) Perform(ctx context.Context, _ queries.FullyQualifiedQueryName, _ queries.Request) (queries.Result[any], error) {
	return queries.Success[any](correlation.FromContext(ctx), "not an Item"), nil
}

func TestTypedPerformFailuresAreReadyCorrelatedAndFailed(t *testing.T) {
	var registry queries.Registry
	calls := 0
	mustRegister(t, queries.Register[Item](&registry, "One", queries.Function(func(context.Context, queries.NoArguments) (Item, error) { calls++; return Item{}, nil })))
	p := build(t, &registry, queries.PipelineOptions{})
	id, err := correlation.Parse("00112233-4455-4677-8899-aabbccddeeff")
	mustRegister(t, err)
	ctx := correlation.WithID(t.Context(), id)
	for _, tc := range []struct {
		name     string
		pipeline queries.Pipeline
		query    queries.FullyQualifiedQueryName
		want     error
	}{
		{"nil pipeline", nil, "Item.One", queries.ErrInvalidRegistration},
		{"unknown query", p, "Item.Missing", queries.ErrUnknownQuery},
		{"runtime mismatch", wrongDataPipeline{p}, "Item.One", queries.ErrResponseType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := queries.Perform[Item](ctx, tc.pipeline, tc.query, queries.Request{})
			if !errors.Is(err, tc.want) || result.IsSuccess() || !result.HasExceptions() || !result.IsReady() || result.Details().CorrelationID != id {
				t.Fatal(result.Details(), err)
			}
			if _, present := result.Data(); present || result.Details().ChangeSet != nil {
				t.Fatal("typed failure retained data or changes")
			}
		})
	}
	result, err := queries.Perform[string](ctx, p, "Item.One", queries.Request{})
	if !errors.Is(err, queries.ErrResponseType) || result.IsSuccess() || !result.IsReady() || result.Details().CorrelationID != id || calls != 0 {
		t.Fatal("static mismatch did not fail before performer", result.Details(), err, calls)
	}
}
