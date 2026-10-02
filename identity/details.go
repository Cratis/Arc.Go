// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package identity

import (
	"context"
	"errors"
	"reflect"
	"slices"
)

var (
	// ErrUnauthenticated identifies an anonymous details caller.
	ErrUnauthenticated = errors.New("identity unauthenticated")
	// ErrDetailsDenied identifies a details provider's application denial.
	ErrDetailsDenied = errors.New("identity details denied")
	// ErrInvalidProvider identifies a nil/typed-nil details provider or context.
	ErrInvalidProvider = errors.New("invalid identity details provider")
)

// Context is an immutable display ID/name and trusted claim snapshot. It confers
// no new authority. Claims returns an independent copy on every call.
type Context struct {
	id     string
	name   string
	claims []Claim
}

// ContextFor copies claims and uses unknown for empty display ID/name, without
// modifying principal or inferring trusted identity from arbitrary claims.
func ContextFor(principal Principal) Context {
	id, name := principal.ID(), principal.Name()
	if id == "" {
		id = "unknown"
	}
	if name == "" {
		name = "unknown"
	}
	return Context{id: id, name: name, claims: principal.Claims()}
}

// ID returns the subject's display identifier, including unknown fallback.
func (c Context) ID() string { return c.id }

// Name returns the display name, including unknown fallback.
func (c Context) Name() string { return c.name }

// Claims returns independent ordered claim pairs.
func (c Context) Claims() []Claim { return slices.Clone(c.claims) }

// Details is provider-owned output. Value is display data, never trusted metadata.
type Details[T any] struct {
	// IsUserAuthorized decides whether the identity view may be provided.
	IsUserAuthorized bool
	// Value is application display data, borrowed by the returned View.
	Value T
}

// DetailsProvider produces display details synchronously. Implementations must
// honor cancellation and support concurrent use if shared; they may not retain ctx.
type DetailsProvider[T any] interface {
	Provide(context.Context, Context) (Details[T], error)
}

// DetailsProviderFunc adapts a details callback.
type DetailsProviderFunc[T any] func(context.Context, Context) (Details[T], error)

// Provide invokes f without installing details into trusted context metadata.
func (f DetailsProviderFunc[T]) Provide(ctx context.Context, value Context) (Details[T], error) {
	return f(ctx, value)
}

// ProvideDetails suppresses providers for anonymous callers, invokes freshly on
// every authenticated call and rejects provider denial. Errors remain inspectable;
// panics belong to the hosting boundary. Roles always come from Principal.
func ProvideDetails[T any](ctx context.Context, provider DetailsProvider[T]) (View[T], error) {
	if ctx == nil {
		return View[T]{}, ErrInvalidProvider
	}
	if err := ctx.Err(); err != nil {
		return View[T]{}, err
	}
	principal, _ := PrincipalFrom(ctx)
	if !principal.IsAuthenticated() {
		return View[T]{}, ErrUnauthenticated
	}
	if provider == nil {
		return View[T]{}, ErrInvalidProvider
	}
	v := reflect.ValueOf(provider)
	switch v.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		if v.IsNil() {
			return View[T]{}, ErrInvalidProvider
		}
	}
	value := ContextFor(principal)
	details, err := provider.Provide(ctx, value)
	if canceled := ctx.Err(); canceled != nil {
		return View[T]{}, canceled
	}
	if err != nil {
		return View[T]{}, err
	}
	if !details.IsUserAuthorized {
		return View[T]{}, ErrDetailsDenied
	}
	roles := principal.Roles()
	if roles == nil {
		roles = []string{}
	}
	return View[T]{ID: value.ID(), Name: value.Name(), IsAuthenticated: true, IsAuthorized: true, Roles: roles, Details: details.Value}, nil
}
