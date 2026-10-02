// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"errors"
	"reflect"
	"time"
)

// Resources is an application-owned holder for operation dependencies. Close
// must honor its context. This will alias fundamentals.go/lifecycle once published.
type Resources = interface{ Close(context.Context) error }

// OpenResources opens cheap/lazy resources, not handler dependencies. A returned
// holder is owned even when accompanied by an error. This will alias
// fundamentals.go/lifecycle once published.
type OpenResources = func(context.Context) (Resources, error)

// ErrResourceType identifies an absent or incompatible resource holder.
var ErrResourceType = errors.New("incompatible operation resources")

// OpenScope captures security before opening resources and rechecks it afterward.
// Nil openers and nil resources yield valid empty scopes; typed nils are invalid.
// Failed opening disposes acquired resources with a detached 30-second cleanup
// budget and joins errors. Panics at application boundaries become PanicError.
func OpenScope(ctx context.Context, open OpenResources) (*Scope, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scope := newScope(ctx, true)
	var err error
	if open != nil {
		err = invoke(ctx, func() error {
			var openErr error
			scope.state.resources, openErr = open(ctx)
			return openErr
		})
	}
	if scope.state.resources != nil && nilResources(scope.state.resources) {
		scope.state.resources = nil
		err = errors.Join(err, ErrResourceType)
	}
	err = errors.Join(err, scope.CheckContext(ctx))
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return nil, errors.Join(err, scope.Close(cleanup))
	}
	return scope, nil
}

// BorrowScope guards resources without taking disposal ownership. Nil means an
// empty holder; typed nil is rejected. The caller remains responsible for keeping
// borrowed resources alive until all uses of the wrapper finish.
func BorrowScope(ctx context.Context, resources Resources) (*Scope, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resources != nil && nilResources(resources) {
		return nil, ErrResourceType
	}
	scope := newScope(ctx, false)
	scope.state.resources = resources
	return scope, nil
}

// Resources returns the borrowed holder or nil for invalid/expired scopes. It is
// a trusted adapter escape hatch, not a revocable security boundary. Use it only
// inside Use; never close it or retain it beyond the callback.
func (s *Scope) Resources() Resources {
	if s == nil || s.state == nil {
		return nil
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.checkLifetime() != nil {
		return nil
	}
	return s.state.resources
}

// ResourcesAs checks lifetime/security and asserts the exact requested holder
// interface/type. It never resolves dependencies. Wrong/absent holders return
// ErrResourceType. Use within an admitted callback to prevent concurrent disposal.
func ResourcesAs[T Resources](ctx context.Context, scope *Scope) (T, error) {
	var zero T
	if err := scope.CheckContext(ctx); err != nil {
		return zero, err
	}
	resources, ok := scope.Resources().(T)
	if !ok {
		return zero, ErrResourceType
	}
	if err := scope.CheckContext(ctx); err != nil {
		return zero, err
	}
	return resources, nil
}

func nilResources(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan:
		return v.IsNil()
	}
	return false
}
