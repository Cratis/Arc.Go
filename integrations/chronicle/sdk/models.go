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
	if model.Descriptor().GoType() != reflect.TypeFor[M]() {
		return integration.ErrInvalid
	}
	return integration.BindReadModel[M](i, string(model.Identifier()))
}

// BindExternalReadModel explicitly opts a registered model into command injection
// when an external producer, rather than the client's projection/reducer, populates it.
func BindExternalReadModel[M any](i *integration.Integration, model readmodels.Model[M]) error {
	if model.Descriptor().GoType() != reflect.TypeFor[M]() {
		return integration.ErrInvalid
	}
	return integration.BindExternalReadModel[M](i, string(model.Identifier()))
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
	store, err := a.client.EventStore(ctx, a.store, chronicle.WithNamespace(chronicle.Namespace(request.Coordinates.Namespace)))
	if err != nil {
		return integration.ModelDocument{}, err
	}
	// SDK GetValue uses the same decoder/collection normalization as Reader.Get.
	// Kernel reads release on the server; passive reducer reads release in the SDK.
	value, err := store.ReadModels().GetValue(auditContext(ctx), reflect.PointerTo(request.Type), readmodels.Key(request.Key))
	if err != nil {
		return integration.ModelDocument{}, err
	}
	instance := reflect.ValueOf(value)
	if !instance.IsValid() || instance.IsNil() {
		return integration.ModelDocument{Released: true}, nil
	}
	return integration.ModelDocument{Value: instance.Elem().Interface(), Exists: true, Released: true}, nil
}
