// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"reflect"
	"slices"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/serialization"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Registrar is shared by handwritten and generated query adapters.
type Registrar interface {
	RegisterReadModel(ModelRegistration) error
	RegisterQuery(Registration) error
}

// Registration holds an immutable typed invocation and metadata snapshot.
type Registration struct {
	descriptor                                    metadata.Query
	descriptorSet                                 bool
	argumentType, returnType, dataType, modelType reflect.Type
	parameters                                    []Parameter
	bind                                          func(Request) (any, error)
	invoke                                        func(context.Context, *Invocation, any) (any, error)
	validators                                    []validatorEntry
	withoutModel                                  bool
	dependencies                                  []di.Key
	renderer                                      *rendererEntry
	page                                          bool
}

// Descriptor returns isolated declaration metadata.
func (r Registration) Descriptor() metadata.Query { return copyDescriptor(r.descriptor) }

// Parameters returns copied caller-input metadata, excluding dependencies.
func (r Registration) Parameters() []Parameter { return slices.Clone(r.parameters) }

// ArgumentType returns the exact argument model type.
func (r Registration) ArgumentType() reflect.Type { return r.argumentType }

// ReturnType returns the performer's raw output type.
func (r Registration) ReturnType() reflect.Type { return r.returnType }

// DataType returns the rendered/unwrapped client data type.
func (r Registration) DataType() reflect.Type { return r.dataType }

// ReadModelType returns the exact owning nonpointer struct.
func (r Registration) ReadModelType() reflect.Type { return r.modelType }

// Register associates a typed performer with one owning read-model query identity.
// Namespace methods are adapted with direct calls; no method discovery or reflect.Call occurs.
func Register[M, A, O any](r Registrar, name string, p Performer[A, O], options ...Option[A]) error {
	if nilValue(r) {
		return ErrInvalidRegistration
	}
	fail := func(err error) error { return &RegistrationError{Identity: name, Kind: err} }
	m := reflect.TypeFor[M]()
	if m.Kind() != reflect.Struct || m.Name() == "" {
		return fail(ErrInvalidRegistration)
	}
	model, err := metadata.InspectModel(m, "")
	if err != nil {
		return fail(err)
	}
	if model.Kind != "" && model.Kind != metadata.ReadModel {
		return fail(ErrInvalidRegistration)
	}
	if err := serialization.ValidateType(m); err != nil {
		return fail(err)
	}
	if p.call == nil {
		return fail(ErrMissingPerformer)
	}
	output := reflect.TypeFor[O]()
	if output.Kind() == reflect.Chan || output.Kind() == reflect.Func {
		return fail(ErrUnsupportedObservable)
	}
	c := queryOptions[A]{descriptor: metadata.Query{ReadModel: model.Type, Name: name, ReadModelPath: model.Path, ReadModelAuthorization: copyAuthorization(model.Authorization), ReadModelIdentityMember: model.IdentityMember, ExcludeFromDiscovery: model.ExcludeFromDiscovery}, seen: map[string]bool{}}
	// Complete descriptor precedes individual overrides regardless of option order.
	for _, o := range options {
		if o == nil {
			return fail(ErrInvalidRegistration)
		}
		if q, ok := o.(queryOption[A]); ok && q.name == "descriptor" {
			if err := o.applyQuery(&c); err != nil {
				return fail(err)
			}
		}
	}
	for _, o := range options {
		if q, ok := o.(queryOption[A]); ok && q.name == "descriptor" {
			continue
		}
		if err := o.applyQuery(&c); err != nil {
			return fail(err)
		}
	}
	if c.descriptor.Observable {
		return fail(ErrUnsupportedObservable)
	}
	bindings, parameters, err := compileArguments(c.arguments)
	if err != nil {
		return fail(err)
	}
	data := output
	page := false
	if output.Kind() == reflect.Pointer && output.Implements(reflect.TypeFor[pageValue]()) {
		return fail(ErrResponseType)
	}
	if pageOutput, ok := any(*new(O)).(pageValue); ok {
		data = pageOutput.pageDataType()
		page = true
	}
	if c.renderer != nil {
		if c.renderer.queryType != output || page {
			return fail(ErrResponseType)
		}
		data = c.renderer.dataType
	}
	// Reusable renderer ownership is validated at Build when registrations are complete.
	if c.renderer != nil || page {
		if !validModelShape(m, data) {
			return fail(ErrResponseType)
		}
	}
	registration := Registration{descriptor: copyDescriptor(c.descriptor), descriptorSet: c.descriptorSet, argumentType: reflect.TypeFor[A](), returnType: output, dataType: data, modelType: m, parameters: parameters, validators: c.validators, withoutModel: c.withoutModel, dependencies: slices.Clone(c.dependencies), renderer: c.renderer, page: page}
	registration.bind = func(request Request) (any, error) { return bindArguments(request, bindings) }
	registration.invoke = func(ctx context.Context, inv *Invocation, a any) (any, error) { return p.call(ctx, inv, a.(A)) }
	return r.RegisterQuery(registration)
}
