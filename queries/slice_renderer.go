// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"reflect"
	"slices"
	"sort"
	"unicode"

	"github.com/cratis/arc.go/internal/modelshape"
)

// SortColumn defines one allowed wire field and a three-way item comparison.
// Compare must not mutate items and must be concurrently callable.
type SortColumn[T any] struct {
	Field   SortField
	Compare func(T, T) int
}

// SliceRenderer opt-in copies membership, counts, stable-sorts, then windows.
// It does not deep-clone models. Plain query slices remain unpaged without it.
type SliceRenderer[T any] struct{ columns []SortColumn[T] }

// NewSliceRenderer rejects duplicate/unknown columns and nil comparisons.
func NewSliceRenderer[T any](columns ...SortColumn[T]) (*SliceRenderer[T], error) {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return nil, ErrInvalidRegistration
	}
	fields, err := modelshape.Fields(t)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field.Name] = true
	}
	seen := map[SortField]bool{}
	aliases := map[string]bool{}
	for _, column := range columns {
		if column.Compare == nil || !allowed[string(column.Field)] || seen[column.Field] || aliases[pascal(string(column.Field))] {
			return nil, ErrInvalidRegistration
		}
		seen[column.Field] = true
		aliases[pascal(string(column.Field))] = true
	}
	return &SliceRenderer[T]{columns: slices.Clone(columns)}, nil
}
func pascal(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

// Execute applies only explicitly registered sort columns. An active unknown field
// produces safe validation. Page count is computed before the window.
func (r *SliceRenderer[T]) Execute(ctx context.Context, items []T, c QueryContext) (RendererResult[[]T], error) {
	if ctx == nil || r == nil {
		return RendererResult[[]T]{}, ErrInvalidRegistration
	}
	if err := ctx.Err(); err != nil {
		return RendererResult[[]T]{}, err
	}
	data := slices.Clone(items)
	total := int64(len(data))
	parameters := c.Parameters()
	sorting := parameters.Sorting
	if sorting.Field != "" && sorting.Direction != Unspecified {
		var compare func(T, T) int
		for _, column := range r.columns {
			if column.Field == sorting.Field || pascal(string(column.Field)) == string(sorting.Field) {
				compare = column.Compare
				break
			}
		}
		if compare == nil || sorting.Direction != Ascending && sorting.Direction != Descending {
			return RendererResult[[]T]{}, &SortingError{Field: "sortby"}
		}
		sort.SliceStable(data, func(i, j int) bool {
			order := compare(data[i], data[j])
			if sorting.Direction == Descending {
				return order > 0
			}
			return order < 0
		})
	}
	if parameters.Paging.IsPaged {
		if findings := parameters.Paging.Validate(); len(findings) > 0 {
			return RendererResult[[]T]{}, &parameterFailure{findings}
		}
		start := min(int64(parameters.Paging.Skip()), int64(len(data)))
		end := min(start+int64(parameters.Paging.Size), int64(len(data)))
		data = data[int(start):int(end)]
	}
	if err := ctx.Err(); err != nil {
		return RendererResult[[]T]{}, err
	}
	return RendererResult[[]T]{Data: data, TotalItems: total}, nil
}
