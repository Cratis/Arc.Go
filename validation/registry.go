// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/cratis/arc.go/execution"
)

var (
	// ErrDuplicate identifies multiple graph validators for one exact type.
	ErrDuplicate = errors.New("duplicate graph validator")
	// ErrFrozen identifies changes after a successful registry Build.
	ErrFrozen = errors.New("validation registry frozen")
	// ErrInvalidRegistration identifies invalid validator model/factory metadata.
	ErrInvalidRegistration = errors.New("invalid validation registration")
	// ErrGraphDepth identifies a graph exceeding the framework traversal budget.
	ErrGraphDepth = errors.New("validation graph depth exceeded")
)

// ModelValidator is the optional model-bound Validate convention. Go method sets
// apply exactly: value input never synthesizes a pointer receiver.
type ModelValidator interface {
	Validate(context.Context) ([]Result, error)
}

// Factory creates a borrowed validator during validation, never during Build.
// The non-closing scope expires on return from this node's validation callback.
// DI dependency manifests will be added when Fundamentals contracts are published.
type Factory[T any] func(context.Context, *execution.Scope) (T, error)

// Registry is a single-owner builder. Zero is usable. One validator (ordinary or
// concept) is allowed per exact type. Build freezes only after successful checks.
type Registry struct {
	entries map[reflect.Type]graphRegistration
	order   []reflect.Type
	frozen  bool
}

type graphRegistration struct {
	concept bool
	invoke  func(context.Context, *execution.Scope, any) ([]Result, error)
}

// Register borrows a shared, concurrently callable validator for exact T.
func Register[T any](r *Registry, validator Validator[T]) error {
	return register(r, validator, Factory[Validator[T]](nil), false)
}

// RegisterConcept registers T as an opaque concept leaf. Findings attach to the
// owning member, not to a concept's private representation.
func RegisterConcept[T any](r *Registry, validator Validator[T]) error {
	return register(r, validator, Factory[Validator[T]](nil), true)
}

// RegisterScoped registers a lazy validator factory; no DI contract is required.
func RegisterScoped[T any](r *Registry, factory Factory[Validator[T]]) error {
	if factory == nil {
		return ErrInvalidRegistration
	}
	return register(r, nil, factory, false)
}

// RegisterScopedConcept registers a lazy concept validator factory.
func RegisterScopedConcept[T any](r *Registry, factory Factory[Validator[T]]) error {
	if factory == nil {
		return ErrInvalidRegistration
	}
	return register(r, nil, factory, true)
}

func register[T any](r *Registry, validator Validator[T], factory Factory[Validator[T]], concept bool) error {
	if r == nil || (factory == nil && isNil(validator)) {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Interface || t.Kind() == reflect.Func || t.Kind() == reflect.Chan || t.Kind() == reflect.UnsafePointer {
		return fmt.Errorf("%w: unsupported model %s", ErrInvalidRegistration, t)
	}
	if _, exists := r.entries[t]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicate, t)
	}
	if r.entries == nil {
		r.entries = make(map[reflect.Type]graphRegistration)
	}
	r.entries[t] = graphRegistration{concept: concept, invoke: func(ctx context.Context, scope *execution.Scope, value any) (results []Result, err error) {
		if factory == nil {
			return Invoke(ctx, validator, value.(T))
		}
		err = scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
			instance, factoryErr := factory(ctx, view)
			if check := view.CheckContext(ctx); check != nil {
				return errors.Join(factoryErr, check)
			}
			if factoryErr != nil {
				return factoryErr
			}
			if isNil(instance) {
				return ErrInvalidValidator
			}
			results, factoryErr = Invoke(ctx, instance, value.(T))
			return factoryErr
		})
		return results, err
	}}
	r.order = append(r.order, t)
	return nil
}

// Build validates statically visible tag shapes without constructing validators
// or invoking rules. Interface-held runtime shapes are checked during Validate.
func (r *Registry) Build() (*Graph, error) {
	if r == nil {
		return nil, ErrInvalidRegistration
	}
	if r.frozen {
		return nil, ErrFrozen
	}
	graph := &Graph{entries: make(map[reflect.Type]graphRegistration)}
	for _, t := range r.order {
		graph.entries[t] = r.entries[t]
	}
	for _, t := range r.order {
		if err := graph.CheckType(t); err != nil {
			return nil, err
		}
	}
	r.frozen = true
	return graph, nil
}
