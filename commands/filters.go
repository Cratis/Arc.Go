// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"

	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Filter contributes a verdict after mandatory authorization. Zero fragments deny.
type Filter interface {
	OnExecution(context.Context, *Invocation) (Result[NoResponse], error)
}

// FilterFunc adapts a synchronous filter callback.
type FilterFunc func(context.Context, *Invocation) (Result[NoResponse], error)

// OnExecution calls f.
func (f FilterFunc) OnExecution(ctx context.Context, inv *Invocation) (Result[NoResponse], error) {
	return f(ctx, inv)
}

// AuthorizationFilter runs ahead of ordinary filters, never instead of declarations.
type AuthorizationFilter interface {
	Filter
	IsAuthorizationFilter()
}
type authorizationFilter struct{ Filter }

func (authorizationFilter) IsAuthorizationFilter() {}

// AsAuthorizationFilter categorizes a filter without activating it.
func AsAuthorizationFilter(filter Filter) AuthorizationFilter {
	if nilValue(filter) {
		return nil
	}
	return authorizationFilter{filter}
}

// AddFilter appends an ordinary lazy filter in stable registration order.
func (r *Registry) AddFilter(name string, factory Factory[Filter], keys ...di.Key) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	return addExtension(r, "filters", name, factory, keys, &r.filters)
}

// AddAuthorizationFilter appends a lazy authorization filter.
func (r *Registry) AddAuthorizationFilter(name string, factory Factory[AuthorizationFilter], keys ...di.Key) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	return addExtension(r, "filters", name, factory, keys, &r.authFilters)
}
