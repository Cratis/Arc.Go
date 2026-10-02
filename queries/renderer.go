// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"reflect"
	"slices"

	"github.com/cratis/arc.go/execution"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// RendererResult supplies transformed data and the pre-window authorized total.
type RendererResult[R any] struct {
	Data       R
	TotalItems int64
}

// Renderer translates exact Q into a supported owning-model data shape.
// Execute must apply row authorization before count/paging; totals are visible data.
type Renderer[Q, R any] interface {
	Execute(context.Context, Q, QueryContext) (RendererResult[R], error)
}

// RendererFunc adapts a synchronous provider rendering callback.
type RendererFunc[Q, R any] func(context.Context, Q, QueryContext) (RendererResult[R], error)

// Execute invokes the rendering callback without taking ownership of Q.
func (f RendererFunc[Q, R]) Execute(ctx context.Context, q Q, c QueryContext) (RendererResult[R], error) {
	return f(ctx, q, c)
}

type rendererEntry struct {
	queryType, dataType reflect.Type
	keys                []di.Key
	invoke              func(context.Context, *execution.Scope, any, QueryContext) (any, int64, error)
}

func makeRenderer[Q, R any](f Factory[Renderer[Q, R]], keys []di.Key) rendererEntry {
	return rendererEntry{queryType: reflect.TypeFor[Q](), dataType: reflect.TypeFor[R](), keys: slices.Clone(keys), invoke: func(ctx context.Context, s *execution.Scope, q any, c QueryContext) (data any, total int64, err error) {
		var renderer Renderer[Q, R]
		if err := boundary.Call(ctx, func(ctx context.Context) error { var err error; renderer, err = f(ctx, s); return err }); err != nil {
			return nil, 0, err
		}
		if nilValue(renderer) {
			return nil, 0, ErrInvalidRegistration
		}
		if err := s.CheckContext(ctx); err != nil {
			return nil, 0, err
		}
		var result RendererResult[R]
		err = boundary.Call(ctx, func(ctx context.Context) error {
			var err error
			result, err = renderer.Execute(ctx, q.(Q), c)
			return err
		})
		if err != nil {
			return nil, 0, err
		}
		return result.Data, result.TotalItems, nil
	}}
}

// RegisterRenderer installs one exact declared-output renderer for reuse. Build
// validates ownership for every query using it without constructing the renderer.
func RegisterRenderer[Q, R any](r *Registry, f Factory[Renderer[Q, R]], keys ...di.Key) error {
	if r == nil || f == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	if r.renderers == nil {
		r.renderers = map[reflect.Type]rendererEntry{}
	}
	key := reflect.TypeFor[Q]()
	if _, ok := r.renderers[key]; ok {
		return ErrDuplicate
	}
	r.renderers[key] = makeRenderer(f, keys)
	return nil
}
