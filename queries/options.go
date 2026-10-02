// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"reflect"
	"slices"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Option is a sealed, typed query registration option. Singular overrides cannot repeat.
type Option[A any] interface{ applyQuery(*queryOptions[A]) error }
type queryOption[A any] struct {
	name   string
	append bool
	apply  func(*queryOptions[A]) error
}
type queryOptions[A any] struct {
	descriptor    metadata.Query
	descriptorSet bool
	seen          map[string]bool
	arguments     []Argument[A]
	validators    []validatorEntry
	withoutModel  bool
	dependencies  []di.Key
	renderer      *rendererEntry
}

func (o queryOption[A]) applyQuery(c *queryOptions[A]) error {
	if !o.append && c.seen[o.name] {
		return ErrDuplicate
	}
	c.seen[o.name] = true
	return o.apply(c)
}

// WithDescriptor replaces the complete query descriptor for stable-client migrations.
func WithDescriptor[A any](d metadata.Query) Option[A] {
	d = copyDescriptor(d)
	return queryOption[A]{name: "descriptor", apply: func(c *queryOptions[A]) error { c.descriptor = copyDescriptor(d); c.descriptorSet = true; return nil }}
}

// WithAuthorization replaces, rather than merges, the model declaration.
func WithAuthorization[A any](a metadata.Authorization) Option[A] {
	a = *copyAuthorization(&a)
	return queryOption[A]{name: "authorization", apply: func(c *queryOptions[A]) error { c.descriptor.Authorization = copyAuthorization(&a); return nil }}
}

// WithPath overrides the method route; empty disables a model path override.
func WithPath[A any](p string) Option[A] {
	return queryOption[A]{name: "path", apply: func(c *queryOptions[A]) error { c.descriptor.Path = &p; return nil }}
}

// WithHTTPMethod restricts the reader method in endpoint metadata.
func WithHTTPMethod[A any](m metadata.QueryHTTPMethod) Option[A] {
	return queryOption[A]{name: "http", apply: func(c *queryOptions[A]) error { c.descriptor.HTTPMethod = m; return nil }}
}

// WithArguments replaces matching compiled field bindings and appends custom bindings.
func WithArguments[A any](a ...Argument[A]) Option[A] {
	a = slices.Clone(a)
	return queryOption[A]{name: "arguments", apply: func(c *queryOptions[A]) error { c.arguments = slices.Clone(a); return nil }}
}

// WithDependencies declares exact DI keys for static catalog validation only.
func WithDependencies[A any](keys ...di.Key) Option[A] {
	keys = slices.Clone(keys)
	return queryOption[A]{name: "dependencies", apply: func(c *queryOptions[A]) error { c.dependencies = slices.Clone(keys); return nil }}
}

// WithoutModelValidation explicitly disables graph/model/tag validation, not explicit validators.
func WithoutModelValidation[A any]() Option[A] {
	return queryOption[A]{name: "without-model", apply: func(c *queryOptions[A]) error { c.withoutModel = true; return nil }}
}

// WithValidator appends a validator in declaration order.
func WithValidator[A any](v validation.Validator[A]) Option[A] {
	return queryOption[A]{name: "validator", append: true, apply: func(c *queryOptions[A]) error {
		if nilValue(v) {
			return ErrInvalidRegistration
		}
		c.validators = append(c.validators, validatorEntry{invoke: func(ctx context.Context, _ *execution.Scope, a any) ([]validation.Result, error) {
			return validation.Invoke(ctx, v, a.(A))
		}})
		return nil
	}}
}

// WithScopedValidator appends a lazy validator factory, constructed only after authorization.
func WithScopedValidator[A any](f Factory[validation.Validator[A]], keys ...di.Key) Option[A] {
	keys = slices.Clone(keys)
	return queryOption[A]{name: "validator", append: true, apply: func(c *queryOptions[A]) error {
		if f == nil {
			return ErrInvalidRegistration
		}
		c.validators = append(c.validators, validatorEntry{keys: keys, invoke: func(ctx context.Context, s *execution.Scope, a any) (findings []validation.Result, err error) {
			v, err := f(ctx, s)
			if err != nil {
				return nil, err
			}
			if nilValue(v) {
				return nil, ErrInvalidRegistration
			}
			return validation.Invoke(ctx, v, a.(A))
		}})
		return nil
	}}
}

// WithRenderer overrides the exact output renderer for this query. Q must equal O.
func WithRenderer[A, Q, R any](f Factory[Renderer[Q, R]], keys ...di.Key) Option[A] {
	entry := makeRenderer(f, keys)
	return queryOption[A]{name: "renderer", apply: func(c *queryOptions[A]) error {
		if f == nil {
			return ErrInvalidRegistration
		}
		c.renderer = &entry
		return nil
	}}
}

type validatorEntry struct {
	keys   []di.Key
	invoke func(context.Context, *execution.Scope, any) ([]validation.Result, error)
}

func validModelShape(model, output reflect.Type) bool {
	if output == model || output.Kind() == reflect.Pointer && output.Elem() == model {
		return true
	}
	if output.Kind() == reflect.Slice || output.Kind() == reflect.Array {
		return output.Elem() == model || output.Elem().Kind() == reflect.Pointer && output.Elem().Elem() == model
	}
	return false
}
