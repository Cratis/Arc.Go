// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"reflect"

	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/readmodels"
)

// BindReadModel contributes a typed command dependency backed by a selected-store
// projection or reducer. Arc query registration is independent. Call before Install.
func BindReadModel[M any](i *integration.Integration, model readmodels.Model[M]) error {
	return bindModel(i, model, false)
}

// BindExternalReadModel explicitly opts a registered model into command injection
// when an external producer, rather than the client's projection/reducer, populates it.
func BindExternalReadModel[M any](i *integration.Integration, model readmodels.Model[M]) error {
	return bindModel(i, model, true)
}
func bindModel[M any](i *integration.Integration, model readmodels.Model[M], external bool) error {
	a, ok := i.ReadModelProvider().(*adapter)
	if !ok || model.Descriptor().GoType() != reflect.TypeFor[M]() {
		return integration.ErrInvalid
	}
	var err error
	if external {
		err = integration.BindExternalReadModel[M](i, string(model.Identifier()))
	} else {
		err = integration.BindReadModel[M](i, string(model.Identifier()))
	}
	if err != nil {
		return err
	}
	a.typedModels[reflect.TypeFor[M]()] = func(ctx context.Context, request integration.ModelRequest) (integration.ModelDocument, error) {
		store, err := a.client.EventStore(ctx, a.store, chronicle.WithNamespace(chronicle.Namespace(request.Coordinates.Namespace)))
		if err != nil {
			return integration.ModelDocument{}, err
		}
		// Kernel reads release on the server; passive reducer reads release in the SDK.
		// The typed SDK reader owns decoding, codecs and collection normalization.
		value, err := readmodels.For(store.ReadModels(), model).Get(auditContext(ctx), readmodels.Key(request.Key))
		if err != nil {
			return integration.ModelDocument{}, err
		}
		result := integration.ModelDocument{Value: value.Value, Exists: value.Exists, Released: true}
		if value.LastHandled != nil {
			position := uint64(*value.LastHandled)
			result.LastHandled = &position
		}
		return result, nil
	}
	return nil
}
func (a *adapter) Model(typ reflect.Type) (integration.ModelDescriptor, bool) {
	descriptor, found := a.models.LookupType(typ)
	if !found {
		return integration.ModelDescriptor{}, false
	}
	_, producer := descriptor.Observer()
	return integration.ModelDescriptor{Type: descriptor.GoType(), ID: string(descriptor.Identifier()), HasProducer: producer != ""}, true
}
func (a *adapter) ReadModel(ctx context.Context, request integration.ModelRequest) (integration.ModelDocument, error) {
	if string(request.Coordinates.Store) != string(a.store) {
		return integration.ModelDocument{}, integration.ErrMismatch
	}
	read, found := a.typedModels[request.Type]
	if !found {
		return integration.ModelDocument{}, integration.ErrNotRegistered
	}
	return read(ctx, request)
}
