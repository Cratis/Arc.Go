// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"slices"

	"github.com/cratis/arc.go/execution"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Filter contributes verdicts only, never query data or change sets. Zero fragments
// deny; successful continuation needs an explicitly authorized fragment.
type Filter interface {
	OnPerform(context.Context, *Invocation) (Result[any], error)
}

// FilterFunc adapts a synchronous filter callback.
type FilterFunc func(context.Context, *Invocation) (Result[any], error)

// OnPerform invokes the filter callback.
func (f FilterFunc) OnPerform(ctx context.Context, i *Invocation) (Result[any], error) {
	return f(ctx, i)
}

// AuthorizationFilter runs before ordinary filter/validator factories. It cannot
// replace the mandatory declaration or tenant membership checks.
type AuthorizationFilter interface {
	Filter
	IsAuthorizationFilter()
}
type authorizationFilter struct{ Filter }

func (authorizationFilter) IsAuthorizationFilter() {}

// AsAuthorizationFilter marks a filter for the authorization stage.
func AsAuthorizationFilter(f Filter) AuthorizationFilter {
	if nilValue(f) {
		return nil
	}
	return authorizationFilter{f}
}

type filterEntry struct {
	name          string
	authorization bool
	factory       Factory[Filter]
	keys          []di.Key
}

// AddFilter appends a named ordinary filter factory. Names are extension identities,
// not DI keys. Instances are borrowed; shared implementations must be concurrent-safe.
func (r *Registry) AddFilter(name string, f Factory[Filter], keys ...di.Key) error {
	return r.addFilter(name, f, false, keys)
}

// AddAuthorizationFilter appends a named authorization-stage filter factory.
func (r *Registry) AddAuthorizationFilter(name string, f Factory[AuthorizationFilter], keys ...di.Key) error {
	if f == nil {
		return ErrInvalidRegistration
	}
	return r.addFilter(name, func(ctx context.Context, s *execution.Scope) (Filter, error) { return f(ctx, s) }, true, keys)
}
func (r *Registry) addFilter(name string, f Factory[Filter], auth bool, keys []di.Key) error {
	if r == nil || name == "" || f == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	for _, entry := range r.filters {
		if entry.name == name {
			return ErrDuplicate
		}
	}
	r.filters = append(r.filters, filterEntry{name: name, authorization: auth, factory: f, keys: slices.Clone(keys)})
	return nil
}
