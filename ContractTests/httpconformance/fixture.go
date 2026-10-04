// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package httpconformance supplies the bounded paired snapshot contract checkpoint.
package httpconformance

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
)

type row struct {
	ID     int    `json:"id"`
	Score  int    `json:"score"`
	Active bool   `json:"active"`
	Label  string `json:"label"`
}

type filter struct {
	Min    int    `json:"min" query:"default=0"`
	Active bool   `json:"active" query:"default=true"`
	Prefix string `json:"prefix" query:"default="`
}

func rows() []row {
	return []row{{3, 10, true, "row-three"}, {1, 30, false, "row-one"}, {4, 20, true, "row-four"}, {2, 40, false, "row-two"}}
}

func application() (*arc.Application, error) {
	b, err := arc.NewBuilder(arc.Options{ExposeExceptionDetails: false})
	if err != nil {
		return nil, err
	}
	renderer, err := queries.NewSliceRenderer(queries.SortColumn[row]{Field: "score", Compare: func(a, b row) int { return cmp.Compare(a.Score, b.Score) }})
	if err != nil {
		return nil, err
	}
	factory := func(context.Context, *execution.Scope) (queries.Renderer[[]row, []row], error) { return renderer, nil }
	if err := queries.Register[row](b, "Plain", queries.Function(func(context.Context, queries.NoArguments) ([]row, error) { return rows(), nil }), queries.WithPath[queries.NoArguments]("/api/plain")); err != nil {
		return nil, err
	}
	if err := queries.Register[row](b, "Renderable", queries.Function(func(context.Context, queries.NoArguments) ([]row, error) { return rows(), nil }), queries.WithPath[queries.NoArguments]("/api/renderable"), queries.WithRenderer[queries.NoArguments](factory)); err != nil {
		return nil, err
	}
	if err := queries.Register[row](b, "Filter", queries.Function(func(_ context.Context, f filter) ([]row, error) {
		return slices.DeleteFunc(rows(), func(r row) bool {
			return r.Score < f.Min || r.Active != f.Active || !strings.HasPrefix(r.Label, f.Prefix)
		}), nil
	}), queries.WithPath[filter]("/api/filter"), queries.WithRenderer[filter](factory)); err != nil {
		return nil, err
	}
	if err := queries.Register[row](b, "Failing", queries.Function(func(context.Context, queries.NoArguments) ([]row, error) { return nil, errors.New("fixture failure") }), queries.WithPath[queries.NoArguments]("/api/failing")); err != nil {
		return nil, err
	}
	return b.Build()
}
