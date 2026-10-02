// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import (
	"context"
	"reflect"
	"slices"
)

// Lifetime determines instance caching and ownership.
type Lifetime uint8

const (
	// Singleton caches once per provider; dependencies are root-owned.
	Singleton Lifetime = iota + 1
	// Scoped caches once per operation scope.
	Scoped
	// Transient creates a new owned value on every resolution.
	Transient
)

type binding struct {
	lifetime     Lifetime
	factory      func(context.Context, *Scope) (any, error)
	dependencies []Key
	borrowed     bool
	value        any
}

// Registry is a single-owner mutable builder. Zero is ready to use.
// A successful Build freezes it; failed validation leaves it editable.
type Registry struct {
	bindings map[Key]binding
	frozen   bool
}

// Bind registers an owned factory and copies its declared direct dependencies.
// Factories must not capture hidden request-bound dependencies in closures.
func Bind[T any](registry *Registry, lifetime Lifetime, factory func(context.Context, *Scope) (T, error), dependencies ...Key) error {
	key := KeyFor[T]()
	if registry == nil {
		return failure("bind", key, nil, ErrInvalidRegistration, nil)
	}
	if registry.frozen {
		return failure("bind", key, nil, ErrFrozen, nil)
	}
	if factory == nil || lifetime < Singleton || lifetime > Transient {
		return failure("bind", key, nil, ErrInvalidRegistration, nil)
	}
	return registry.add(key, binding{lifetime: lifetime, dependencies: slices.Clone(dependencies), factory: func(ctx context.Context, s *Scope) (any, error) { return factory(ctx, s) }})
}

// BindValue registers a borrowed singleton. Arc never closes the supplied value.
func BindValue[T any](registry *Registry, value T) error {
	key := KeyFor[T]()
	if registry == nil {
		return failure("bind value", key, nil, ErrInvalidRegistration, nil)
	}
	if registry.frozen {
		return failure("bind value", key, nil, ErrFrozen, nil)
	}
	if nilValue(value) {
		return failure("bind value", key, nil, ErrInvalidRegistration, ErrNilValue)
	}
	return registry.add(key, binding{lifetime: Singleton, borrowed: true, value: value})
}
func (r *Registry) add(key Key, b binding) error {
	if _, exists := r.bindings[key]; exists {
		return failure("bind", key, nil, ErrDuplicate, nil)
	}
	seen := map[Key]bool{}
	for _, dep := range b.dependencies {
		if dep.typ == nil {
			return failure("bind", key, nil, ErrInvalidRegistration, nil)
		}
		if seen[dep] {
			return failure("bind", key, []Key{key, dep}, ErrDuplicate, nil)
		}
		seen[dep] = true
	}
	if r.bindings == nil {
		r.bindings = map[Key]binding{}
	}
	r.bindings[key] = b
	return nil
}

// Build validates the declared graph without factories, I/O, cleanup, or goroutines.
// An empty registry builds successfully. Success freezes all further registration/build.
func (r *Registry) Build() (*Provider, error) {
	if r == nil {
		return nil, failure("build", Key{}, nil, ErrInvalidRegistration, nil)
	}
	if r.frozen {
		return nil, failure("build", Key{}, nil, ErrFrozen, nil)
	}
	if err := validateGraph(r.bindings); err != nil {
		return nil, err
	}
	bindings := make(map[Key]binding, len(r.bindings))
	for key, b := range r.bindings {
		b.dependencies = slices.Clone(b.dependencies)
		bindings[key] = b
	}
	r.frozen = true
	return &Provider{bindings: bindings}, nil
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}

// Provider owns singleton instances and outstanding child scopes. Zero is invalid.
// Construct with Registry.Build; methods added alongside scope resolution.
type Provider struct{ bindings map[Key]binding }

// Scope owns scoped values and transient instances. Zero is invalid.
// Construct with Provider.NewScope; factory views are restricted and expire.
type Scope struct{}
