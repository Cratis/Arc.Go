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

// ReadModelInterceptor transforms one exact pointer/value model after rendering.
// Masking cannot secure leaked counts: row authorization must happen before paging.
type ReadModelInterceptor[T any] interface {
	Intercept(context.Context, T) (T, error)
}

// InterceptorFunc adapts an ordered synchronous transformation.
type InterceptorFunc[T any] func(context.Context, T) (T, error)

// Intercept invokes the transformation callback.
func (f InterceptorFunc[T]) Intercept(ctx context.Context, t T) (T, error) { return f(ctx, t) }

type interceptorEntry struct {
	name   string
	typ    reflect.Type
	keys   []di.Key
	create func(context.Context, *execution.Scope) (func(context.Context, any) (any, error), error)
}

// RegisterReadModelInterceptor appends an exact typed, named interceptor. Each query
// activates applicable factories only after rendering. Items and interceptors run
// sequentially in registration order. No automatic open-generic closure is performed.
func RegisterReadModelInterceptor[T any](r *Registry, name string, f Factory[ReadModelInterceptor[T]], keys ...di.Key) error {
	if r == nil || name == "" || f == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	t := reflect.TypeFor[T]()
	base := t
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct || base.Name() == "" {
		return ErrInvalidRegistration
	}
	for _, entry := range r.interceptors {
		if entry.typ == t && entry.name == name {
			return ErrDuplicate
		}
	}
	r.interceptors = append(r.interceptors, interceptorEntry{name: name, typ: t, keys: slices.Clone(keys), create: func(ctx context.Context, s *execution.Scope) (func(context.Context, any) (any, error), error) {
		interceptor, err := f(ctx, s)
		if err != nil {
			return nil, err
		}
		if nilValue(interceptor) {
			return nil, ErrInvalidRegistration
		}
		return func(ctx context.Context, value any) (any, error) { return interceptor.Intercept(ctx, value.(T)) }, nil
	}})
	return nil
}
func intercept(ctx context.Context, s *execution.Scope, data any, typ reflect.Type, entries []interceptorEntry) (any, error) {
	if nilValue(data) {
		return data, nil
	}
	collection := typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array
	itemType := typ
	if collection {
		itemType = typ.Elem()
	}
	var callbacks []func(context.Context, any) (any, error)
	for _, entry := range entries {
		if entry.typ != itemType {
			continue
		}
		var callback func(context.Context, any) (any, error)
		err := boundary.Call(ctx, func(ctx context.Context) error { var err error; callback, err = entry.create(ctx, s); return err })
		if err != nil {
			return nil, err
		}
		if err := s.CheckContext(ctx); err != nil {
			return nil, err
		}
		callbacks = append(callbacks, callback)
	}
	if len(callbacks) == 0 {
		return data, nil
	}
	transform := func(value any) (any, error) {
		if nilValue(value) {
			return value, nil
		}
		for _, callback := range callbacks {
			var next any
			err := boundary.Call(ctx, func(ctx context.Context) error { var err error; next, err = callback(ctx, value); return err })
			if err != nil {
				return nil, err
			}
			if err := s.CheckContext(ctx); err != nil {
				return nil, err
			}
			value = next
			if nilValue(value) {
				break
			}
		}
		return value, nil
	}
	if !collection {
		return transform(data)
	}
	original := reflect.ValueOf(data)
	var copy reflect.Value
	if typ.Kind() == reflect.Array {
		copy = reflect.New(typ).Elem()
		copy.Set(original)
	} else {
		copy = reflect.MakeSlice(typ, original.Len(), original.Len())
		reflect.Copy(copy, original)
	}
	for i := 0; i < copy.Len(); i++ {
		value, err := transform(copy.Index(i).Interface())
		if err != nil {
			return nil, err
		}
		if value == nil {
			copy.Index(i).SetZero()
		} else {
			copy.Index(i).Set(reflect.ValueOf(value))
		}
	}
	return copy.Interface(), nil
}
