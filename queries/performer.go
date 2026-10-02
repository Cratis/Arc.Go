// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"

	"github.com/cratis/arc.go/execution"
)

// QueryName is a model-local query name.
type QueryName string

// FullyQualifiedQueryName is the stable model identity followed by the query name.
type FullyQualifiedQueryName string

// NoArguments declares a query without caller inputs.
type NoArguments struct{}

// Factory lazily obtains a borrowed stage collaborator. It receives an expiring,
// non-closing operation view and must not retain it.
type Factory[T any] func(context.Context, *execution.Scope) (T, error)

// Performer is an immutable typed adapter. Zero has no callback. Shared captured
// dependencies must support concurrent calls; no adapter activates at Build.
type Performer[A, O any] struct {
	call func(context.Context, *Invocation, A) (O, error)
}

// Function adapts an ordinary function or dependency-capturing closure.
func Function[A, O any](f func(context.Context, A) (O, error)) Performer[A, O] {
	if f == nil {
		return Performer[A, O]{}
	}
	return Invoke(func(ctx context.Context, _ *Invocation, a A) (O, error) { return f(ctx, a) })
}

// WithParameters supplies typed paging/sorting without ambient context lookup.
func WithParameters[A, O any](f func(context.Context, A, Parameters) (O, error)) Performer[A, O] {
	if f == nil {
		return Performer[A, O]{}
	}
	return Invoke(func(ctx context.Context, inv *Invocation, a A) (O, error) {
		return f(ctx, a, inv.QueryContext().Parameters())
	})
}

// Invoke supplies the public generation/manual scope seam. It runs after validation.
func Invoke[A, O any](f func(context.Context, *Invocation, A) (O, error)) Performer[A, O] {
	return Performer[A, O]{call: f}
}

// Scoped lazily adapts application-owned resource holders, without requiring DI.
func Scoped[A, O any, S execution.Resources](f func(context.Context, S) (Performer[A, O], error)) Performer[A, O] {
	if f == nil {
		return Performer[A, O]{}
	}
	return Invoke(func(ctx context.Context, inv *Invocation, a A) (zero O, err error) {
		holder, err := execution.ResourcesAs[S](ctx, inv.Scope())
		if err != nil {
			return zero, err
		}
		performer, err := f(ctx, holder)
		if err != nil {
			return zero, err
		}
		if performer.call == nil {
			return zero, ErrMissingPerformer
		}
		if err := inv.Scope().CheckContext(ctx); err != nil {
			return zero, err
		}
		return performer.call(ctx, inv, a)
	})
}
