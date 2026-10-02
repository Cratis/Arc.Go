// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"reflect"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// ResponseValueHandler is an additive server consumer. Every matching handler
// runs in registration order. CanHandle may depend on the selected response.
type ResponseValueHandler interface {
	CanHandle(CommandContext, any) bool
	Handle(context.Context, *Invocation, any) (Result[NoResponse], error)
}

// ResponseValueContextUpdater participates before response selection. It may set
// context values, never identity, tenant, correlation, resources or ownership.
type ResponseValueContextUpdater interface {
	ResponseValueHandler
	UpdateContext(context.Context, *Invocation, any) error
}

// AddResponseValueHandler registers a lazy dynamic consumer.
func (r *Registry) AddResponseValueHandler(name string, factory Factory[ResponseValueHandler], keys ...di.Key) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	return addExtension(r, "responses", name, factory, keys, &r.responses)
}

// RegisterResponseValueHandler limits a consumer to exact concrete T or values
// implementing interface T, retaining its dynamic predicate. The marker does not
// establish unconditional no-response metadata.
func RegisterResponseValueHandler[T any](r *Registry, name string, factory Factory[ResponseValueHandler], keys ...di.Key) error {
	if factory == nil {
		return ErrInvalidRegistration
	}
	return r.AddResponseValueHandler(name, func(ctx context.Context, scope *execution.Scope) (ResponseValueHandler, error) {
		h, err := factory(ctx, scope)
		if err != nil {
			return nil, err
		}
		if nilValue(h) {
			return nil, ErrInvalidRegistration
		}
		base := typedConsumer{handler: h, typ: reflect.TypeFor[T]()}
		if updater, ok := h.(ResponseValueContextUpdater); ok {
			return typedUpdater{typedConsumer: base, updater: updater}, nil
		}
		return base, nil
	}, keys...)
}

type typedConsumer struct {
	handler ResponseValueHandler
	typ     reflect.Type
}

func (h typedConsumer) CanHandle(c CommandContext, v any) bool {
	return matchesConsumerType(h.typ, reflect.TypeOf(v)) && h.handler.CanHandle(c, v)
}
func matchesConsumerType(consumerType, valueType reflect.Type) bool {
	if valueType == nil {
		return false
	}
	if consumerType.Kind() == reflect.Interface {
		return valueType.Implements(consumerType)
	}
	return consumerType == valueType
}
func (h typedConsumer) Handle(ctx context.Context, inv *Invocation, value any) (Result[NoResponse], error) {
	return h.handler.Handle(ctx, inv, value)
}

type typedUpdater struct {
	typedConsumer
	updater ResponseValueContextUpdater
}

func (h typedUpdater) UpdateContext(ctx context.Context, inv *Invocation, value any) error {
	return h.updater.UpdateContext(ctx, inv, value)
}

func builtinControl(leaf outcomeLeaf) (Result[NoResponse], bool) {
	if leaf.kind == controlLeaf {
		return leaf.value.(Result[NoResponse]), true
	}
	switch v := leaf.value.(type) {
	case validation.Result:
		return WithValidationResults(correlation.ID{}, v), true
	case authorization.Decision:
		if v.IsAllowed() {
			return Success(correlation.ID{}), true
		}
		return Unauthorized(correlation.ID{}, v.Reason()), true
	}
	return Result[NoResponse]{}, false
}
func (f *frame) matches(handler ResponseValueHandler, value any) (bool, error) {
	var match bool
	err := f.call(func(context.Context, *Invocation) error { match = handler.CanHandle(f.snapshot, value); return nil })
	return match, err
}
func (f *frame) consumers(handlers []ResponseValueHandler, leaf outcomeLeaf) ([]ResponseValueHandler, error) {
	var result []ResponseValueHandler
	for _, h := range handlers {
		match, err := f.matches(h, leaf.value)
		if err != nil {
			return nil, err
		}
		if match {
			result = append(result, h)
		}
	}
	return result, nil
}
func (f *frame) process(output any) {
	var leaves []outcomeLeaf
	if err := flatten(output, valueLeaf, 0, &leaves); err != nil {
		f.fail(err, false)
		return
	}
	if len(leaves) == 0 {
		return
	}
	var handlers []ResponseValueHandler
	for _, entry := range f.pipeline.responses {
		h, err := activate(f, entry)
		f.fail(err, false)
		if err != nil {
			return
		}
		handlers = append(handlers, h)
	}
	// Updaters see all leaves before a response is selected.
	for _, leaf := range leaves {
		for _, h := range handlers {
			updater, ok := h.(ResponseValueContextUpdater)
			if !ok {
				continue
			}
			match, err := f.matches(h, leaf.value)
			f.fail(err, false)
			if err != nil {
				return
			}
			if match {
				err = f.call(func(ctx context.Context, inv *Invocation) error { return updater.UpdateContext(ctx, inv, leaf.value) })
				f.fail(err, false)
				if err != nil {
					return
				}
			}
		}
	}
	// Reserve explicit responses before evaluating context-sensitive predicates.
	selected := -1
	for i, leaf := range leaves {
		if leaf.kind == responseLeaf {
			if selected >= 0 {
				f.fail(ErrMultipleResponses, false)
				return
			}
			selected = i
			f.snapshot.response, f.snapshot.hasResponse = leaf.value, true
		}
	}
	for i, leaf := range leaves {
		if leaf.kind != valueLeaf {
			continue
		}
		if _, control := builtinControl(leaf); control {
			continue
		}
		matching, err := f.consumers(handlers, leaf)
		f.fail(err, false)
		if err != nil {
			return
		}
		if len(matching) == 0 {
			if selected >= 0 {
				f.fail(ErrMultipleResponses, false)
				return
			}
			selected = i
			f.snapshot.response, f.snapshot.hasResponse = leaf.value, true
		}
	}
	if f.snapshot.hasResponse {
		if f.registration.responseKind == ResponseNone || (f.registration.responseKind == ResponseValue && !reflect.TypeOf(f.snapshot.response).AssignableTo(f.registration.responseType)) {
			f.fail(ErrResponseType, false)
			return
		}
	}
	// Reevaluate with the selected response and preflight every unconsumed effect
	// before running a consumer. Controls always precede external effects.
	matches := make([][]ResponseValueHandler, len(leaves))
	for i, leaf := range leaves {
		if i == selected {
			continue
		}
		if control, ok := builtinControl(leaf); ok {
			f.merge(control, false)
			continue
		}
		var err error
		matches[i], err = f.consumers(handlers, leaf)
		f.fail(err, false)
		if err != nil {
			return
		}
		if len(matches[i]) == 0 {
			f.fail(ErrUnhandledEffect, false)
			return
		}
	}
	if !f.result.IsSuccess() {
		return
	}
	for i, leaf := range leaves {
		for _, handler := range matches[i] {
			var fragment Result[NoResponse]
			err := f.call(func(ctx context.Context, inv *Invocation) error {
				var err error
				fragment, err = handler.Handle(ctx, inv, leaf.value)
				return err
			})
			if err == nil {
				f.merge(fragment, false)
			}
			f.fail(err, false)
			if err != nil {
				return
			}
		}
	}
}
