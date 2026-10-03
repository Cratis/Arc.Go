// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"reflect"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type preparedCall struct {
	control    Result[NoResponse]
	hasControl bool
	stop       bool
	handle     func(context.Context, *Invocation) (any, error)
}
type adapter struct {
	prepare      func(context.Context, *Invocation, any) (preparedCall, error)
	returnType   reflect.Type
	responseType reflect.Type
	responseKind ResponseKind
	valid        bool
}

// Handler contains immutable, separately staged command callbacks. Callbacks and
// captured dependencies must support concurrent calls. O is the raw return type.
type Handler[C, O any] struct{ adapter adapter }

func (h Handler[C, O]) apply(c *configuration[C]) error {
	if c.handler != nil {
		return ErrDuplicate
	}
	if !h.adapter.valid {
		return ErrMissingHandler
	}
	a := h.adapter
	c.handler = &a
	return nil
}

func returnAdapter[O any]() adapter {
	t := reflect.TypeFor[O]()
	a := adapter{returnType: t, valid: true, responseKind: ResponseUnknown}
	var zero O
	if operationReturn(t) || t == reflect.TypeFor[NoResponse]() || t == reflect.TypeFor[validation.Result]() || t == reflect.TypeFor[authorization.Decision]() {
		a.responseKind = ResponseNone
	}
	if contract, ok := any(zero).(interface{ responseContract() reflect.Type }); ok {
		a.responseType = contract.responseContract()
		a.responseKind = ResponseValue
		if a.responseType == reflect.TypeFor[NoResponse]() {
			a.responseKind = ResponseNone
		}
	}
	return a
}

// Handle adapts a model method expression or an ordinary typed callback.
func Handle[C, O any](call func(C, context.Context) (O, error)) Handler[C, O] {
	return Invoke(func(ctx context.Context, _ *Invocation, c C) (O, error) { return call(c, ctx) }).withValidity(call != nil)
}

func (h Handler[C, O]) withValidity(valid bool) Handler[C, O] { h.adapter.valid = valid; return h }

// Void adapts an error-only method expression.
func Void[C any](call func(C, context.Context) error) Handler[C, NoResponse] {
	return Handle(func(c C, ctx context.Context) (NoResponse, error) { return NoResponse{}, call(c, ctx) }).withValidity(call != nil)
}

// Invoke supplies an expiring callback frame for generated and handwritten adapters.
func Invoke[C, O any](call func(context.Context, *Invocation, C) (O, error)) Handler[C, O] {
	a := returnAdapter[O]()
	a.valid = call != nil
	a.prepare = func(_ context.Context, _ *Invocation, value any) (preparedCall, error) {
		return preparedCall{handle: func(ctx context.Context, inv *Invocation) (any, error) { return call(ctx, inv, value.(C)) }}, nil
	}
	return Handler[C, O]{adapter: a}
}

// WithProvide separates typed preparation from handling. A failed Provide never
// fabricates a usable payload. Neither callback is called by Validate.
func WithProvide[C, P, O any](provide func(C, context.Context) (P, error), handle func(C, context.Context, P) (O, error)) Handler[C, O] {
	return WithPreparation(func(c C, ctx context.Context) (Preparation[P], error) {
		p, err := provide(c, ctx)
		if err != nil {
			return Preparation[P]{}, err
		}
		return Provided(p), nil
	}, handle).withValidity(provide != nil && handle != nil)
}

// WithPreparation supports explicit preparation controls and early stop.
func WithPreparation[C, P, O any](provide func(C, context.Context) (Preparation[P], error), handle func(C, context.Context, P) (O, error)) Handler[C, O] {
	return Prepare(func(ctx context.Context, _ *Invocation, c C) (Preparation[P], error) { return provide(c, ctx) },
		func(ctx context.Context, _ *Invocation, c C, p P) (O, error) { return handle(c, ctx, p) }).withValidity(provide != nil && handle != nil)
}

// Prepare is the generator seam: dependencies resolve independently in each stage.
func Prepare[C, P, O any](provide func(context.Context, *Invocation, C) (Preparation[P], error), handle func(context.Context, *Invocation, C, P) (O, error)) Handler[C, O] {
	a := returnAdapter[O]()
	a.valid = provide != nil && handle != nil
	a.prepare = func(ctx context.Context, inv *Invocation, value any) (preparedCall, error) {
		c := value.(C)
		p, err := provide(ctx, inv, c)
		if err != nil {
			return preparedCall{}, err
		}
		if !p.valid {
			return preparedCall{}, ErrInvalidPreparation
		}
		return preparedCall{control: p.control, hasControl: true, stop: p.stop, handle: func(ctx context.Context, inv *Invocation) (any, error) { return handle(ctx, inv, c, p.value) }}, nil
	}
	return Handler[C, O]{adapter: a}
}

// Scoped lazily constructs an adapter from an application resource holder after
// authorization and validation. It preserves the adapter's preparation stage.
func Scoped[C, O any, S execution.Resources](factory func(context.Context, S) (Handler[C, O], error)) Handler[C, O] {
	a := returnAdapter[O]()
	a.valid = factory != nil
	a.prepare = func(ctx context.Context, inv *Invocation, value any) (preparedCall, error) {
		resources, err := execution.ResourcesAs[S](ctx, inv.Scope())
		if err != nil {
			return preparedCall{}, err
		}
		h, err := factory(ctx, resources)
		if err != nil {
			return preparedCall{}, err
		}
		if !h.adapter.valid {
			return preparedCall{}, ErrMissingHandler
		}
		return h.adapter.prepare(ctx, inv, value)
	}
	return Handler[C, O]{adapter: a}
}
