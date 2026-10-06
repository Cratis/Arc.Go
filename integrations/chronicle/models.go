// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"reflect"

	"github.com/cratis/arc.go/commands"
)

// ModelDescriptor identifies selected-store ownership, independently of Arc query registration.
type ModelDescriptor struct {
	Type        reflect.Type
	ID          string
	HasProducer bool
}

// ModelRequest fixes model, key and tenant coordinates for a one-shot read.
type ModelRequest struct {
	Coordinates Coordinates
	Type        reflect.Type
	Key         string
}

// ModelDocument preserves absence and optional progress. Released must be true for
// present values: the owning reader performs release exactly once before returning.
// LastHandled is diagnostic progress, never a protected-decision token.
type ModelDocument struct {
	Value       any
	Exists      bool
	LastHandled *uint64
	Released    bool
}

// ModelReader owns typed materialization, normalization and compliance release.
// No read-model watch, protected decision or resource-retaining session is implied.
type ModelReader interface {
	Model(reflect.Type) (ModelDescriptor, bool)
	ReadModel(context.Context, ModelRequest) (ModelDocument, error)
}

type modelCacheKey struct {
	coordinates Coordinates
	key         string
	model       reflect.Type
}

// ReadModelProvider borrows the configured provider for typed adapter binding.
// It grants no command/transaction ownership. Configure it only before Install.
func (i *Integration) ReadModelProvider() ModelReader {
	if i == nil {
		return nil
	}
	return i.options.Models
}

// BindReadModel declares a producer-backed command dependency. A query marker
// alone is insufficient. Handwritten/generated adapters use commands.RequireReadModel.
func BindReadModel[M any](i *Integration, id string) error { return bindModel[M](i, id, false) }

// BindExternalReadModel explicitly binds a registered externally produced model.
// The caller, not an Arc query declaration, asserts that its producer exists.
func BindExternalReadModel[M any](i *Integration, id string) error { return bindModel[M](i, id, true) }
func bindModel[M any](i *Integration, id string, external bool) error {
	if i == nil || isNil(i.options.Models) {
		return ErrInvalid
	}
	if i.installed {
		return commands.ErrFrozen
	}
	descriptor, ok := i.options.Models.Model(reflect.TypeFor[M]())
	if !ok || descriptor.ID != id || (!external && !descriptor.HasProducer) {
		return ErrNotRegistered
	}
	i.bindings = append(i.bindings, func(registry *commands.Registry) error {
		return commands.RegisterReadModelProvider[M](registry, "chronicle:"+id, commands.DeclaredReadModel, func(ctx context.Context, inv *commands.Invocation, key string) (commands.ReadModelInstance[M], error) {
			var result commands.ReadModelInstance[M]
			frame, err := i.frameFor(ctx, inv)
			if err != nil {
				return result, err
			}
			document, err := i.readModel(ctx, inv, frame, reflect.TypeFor[M](), key)
			if err != nil {
				return result, err
			}
			if document.Exists {
				value, ok := document.Value.(M)
				if !ok {
					return result, ErrInvalid
				}
				result.Value, result.Exists = value, true
			}
			return result, nil
		})
	})
	return nil
}
func (i *Integration) readModel(ctx context.Context, inv *commands.Invocation, frame *commandFrame, model reflect.Type, key string) (ModelDocument, error) {
	cacheKey := modelCacheKey{coordinates: frame.coordinates, key: key, model: model}
	frame.modelMu.Lock()
	if frame.models == nil {
		frame.models = map[modelCacheKey]ModelDocument{}
		frame.modelBusy = map[modelCacheKey]bool{}
	}
	if document, found := frame.models[cacheKey]; found {
		frame.modelMu.Unlock()
		return document, nil
	}
	if frame.modelBusy[cacheKey] {
		frame.modelMu.Unlock()
		return ModelDocument{}, ErrConcurrent
	}
	frame.modelBusy[cacheKey] = true
	frame.modelMu.Unlock()
	defer func() { frame.modelMu.Lock(); delete(frame.modelBusy, cacheKey); frame.modelMu.Unlock() }()
	document, err := i.options.Models.ReadModel(frame.context(ctx), ModelRequest{Coordinates: frame.coordinates, Type: model, Key: key})
	if err != nil {
		return ModelDocument{}, err
	}
	if document.Exists && (!document.Released || reflect.TypeOf(document.Value) != model) {
		return ModelDocument{}, ErrInvalid
	}
	if err := inv.Execution().Check(ctx); err != nil {
		return ModelDocument{}, err
	}
	frame.modelMu.Lock()
	frame.models[cacheKey] = document
	frame.modelMu.Unlock()
	return document, nil
}
