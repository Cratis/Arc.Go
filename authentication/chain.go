// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authentication

import (
	"context"
	"net/http"
	"reflect"
	"slices"

	"github.com/cratis/arc.go/identity"
)

// Handler authenticates synchronously, borrowing request data for the call only.
// Handlers must not read Body: it is shared with the caller, not cloned.
// Shared handlers must support concurrent calls and honor cancellation.
type Handler interface {
	Authenticate(context.Context, *http.Request) (Result, error)
}

// HandlerFunc adapts a trusted authentication callback.
type HandlerFunc func(context.Context, *http.Request) (Result, error)

// Authenticate invokes f with borrowed request data.
func (f HandlerFunc) Authenticate(ctx context.Context, request *http.Request) (Result, error) {
	return f(ctx, request)
}

// Chain is immutable and concurrent-safe when its borrowed handlers are. Zero is
// an empty chain. No callback is run after a terminal success or failure.
type Chain struct{ handlers []Handler }

// New copies the registration slice, rejecting nil and typed-nil handlers.
func New(handlers ...Handler) (*Chain, error) {
	for _, handler := range handlers {
		if nilHandler(handler) {
			return nil, ErrInvalidHandler
		}
	}
	return &Chain{handlers: slices.Clone(handlers)}, nil
}

// Authenticate installs the supplied context on a request clone. Context, headers
// and URL are isolated from the caller; Body is shared and handlers must not read
// it. Other request data is not guaranteed isolated. Errors are infrastructure
// failures, not credential rejection.
func (c *Chain) Authenticate(ctx context.Context, request *http.Request) (Result, error) {
	if c == nil || ctx == nil || request == nil {
		return Result{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	request = request.Clone(ctx)
	for _, handler := range c.handlers {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		result, err := handler.Authenticate(ctx, request)
		if canceled := ctx.Err(); canceled != nil {
			return Result{}, canceled
		}
		if err != nil {
			return Result{}, err
		}
		if _, ok := result.Principal(); ok || result.Failure() != nil {
			return result, nil
		}
	}
	return Anonymous(), nil
}

// HostPrincipal reads only trusted typed identity context metadata. It does not
// authenticate headers, JWT payloads, Basic credentials or display cookies.
func HostPrincipal() Handler {
	return HandlerFunc(func(ctx context.Context, _ *http.Request) (Result, error) {
		principal, ok := identity.PrincipalFrom(ctx)
		if !ok || !principal.IsAuthenticated() {
			return Anonymous(), nil
		}
		return Authenticated(principal)
	})
}
func nilHandler(handler Handler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		return value.IsNil()
	}
	return false
}
