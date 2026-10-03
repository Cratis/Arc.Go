// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/metadata"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// ResponseKind distinguishes an unresolved contract from guaranteed absence.
type ResponseKind uint8

const (
	// ResponseUnknown requires an explicit contract for typed execution.
	ResponseUnknown ResponseKind = iota
	// ResponseNone guarantees no client response.
	ResponseNone
	// ResponseValue permits a response of the declared type, or absence.
	ResponseValue
)

// Registrar accepts frozen registrations; generated adapters use this seam.
type Registrar interface{ RegisterCommand(Registration) error }

// Registration is an immutable typed command adapter and descriptor.
type Registration struct {
	descriptor       metadata.Command
	commandType      reflect.Type
	adapter          adapter
	decode           func([]byte) (any, error)
	key              func(any) (string, bool, error)
	validators       []validatorEntry
	withoutModel     bool
	dependencies     []di.Key
	responseKind     ResponseKind
	responseType     reflect.Type
	responseOverride bool
}

// Descriptor returns a copy-isolated declaration.
func (r Registration) Descriptor() metadata.Command { return cloneDescriptor(r.descriptor) }

// CommandType returns the exact pointer or value registration type.
func (r Registration) CommandType() reflect.Type { return r.commandType }

// ReturnType returns Handle's raw declared result type.
func (r Registration) ReturnType() reflect.Type { return r.adapter.returnType }

// ResponseKind returns the frozen client contract classification.
func (r Registration) ResponseKind() ResponseKind { return r.responseKind }

// ResponseType returns a known response type, never the raw type as a guess.
func (r Registration) ResponseType() (reflect.Type, bool) {
	return r.responseType, r.responseKind == ResponseValue
}

// Decode allocates fresh input and uses Arc scalar/field serialization.
func (r Registration) Decode(body []byte) (any, error) {
	if r.decode == nil {
		return nil, ErrInvalidRegistration
	}
	return r.decode(body)
}

// Register composes one handler and ordered typed options. Without a handler it
// supports exactly Handle(context.Context) error through Go's method set.
func Register[C any](r Registrar, options ...Option[C]) error {
	if nilValue(r) {
		return ErrInvalidRegistration
	}
	namespace := ""
	if defaults, ok := r.(interface{ CommandNamespace() string }); ok {
		namespace = defaults.CommandNamespace()
	} else if defaults, ok := r.(interface{ commandNamespace() string }); ok {
		namespace = defaults.commandNamespace()
	}
	t := reflect.TypeFor[C]()
	model, err := metadata.InspectModel(t, namespace)
	if err != nil {
		return &RegistrationError{Kind: errors.Join(ErrInvalidRegistration, err)}
	}
	if model.Kind == metadata.ReadModel {
		return ErrInvalidRegistration
	}
	c := configuration[C]{descriptor: metadata.Command{Type: model.Type, Path: model.Path, Authorization: model.Authorization, BlockOnValidationSeverity: model.BlockOnValidationSeverity, ExcludeFromDiscovery: model.ExcludeFromDiscovery}, seen: make(map[string]bool)}
	// A complete descriptor precedes individual overrides regardless of option order.
	for _, opt := range options {
		if nilValue(opt) {
			return ErrInvalidRegistration
		}
		if o, ok := opt.(option[C]); ok && o.name == "descriptor" {
			if err := o.apply(&c); err != nil {
				return err
			}
		}
	}
	for _, opt := range options {
		if o, ok := opt.(option[C]); ok && o.name == "descriptor" {
			continue
		}
		if err := opt.apply(&c); err != nil {
			return &RegistrationError{c.descriptor.Type.Identity(), err}
		}
	}
	if c.handler == nil {
		if !t.Implements(reflect.TypeFor[interface{ Handle(context.Context) error }]()) {
			return &RegistrationError{c.descriptor.Type.Identity(), ErrMissingHandler}
		}
		h := Void(func(value C, ctx context.Context) error {
			return any(value).(interface{ Handle(context.Context) error }).Handle(ctx)
		})
		c.handler = &h.adapter
	}
	merged := metadata.Model{Kind: metadata.CommandModel, Type: c.descriptor.Type, Path: c.descriptor.Path, Authorization: c.descriptor.Authorization, BlockOnValidationSeverity: c.descriptor.BlockOnValidationSeverity}
	if err := merged.Validate(); err != nil {
		return &RegistrationError{c.descriptor.Type.Identity(), errors.Join(ErrInvalidRegistration, err)}
	}
	key := taggedKey(t, model.KeyMember)
	if c.key != nil {
		key = func(value any) (string, bool, error) { key, present := c.key(value.(C)); return key, present, nil }
	}
	registration := Registration{descriptor: cloneDescriptor(c.descriptor), commandType: t, adapter: *c.handler, decode: decodeCommand[C], key: key, validators: slices.Clone(c.validators), withoutModel: c.withoutModel, dependencies: slices.Clone(c.dependencies), responseKind: c.handler.responseKind, responseType: c.handler.responseType, responseOverride: c.responseOverride}
	if c.responseOverride {
		registration.responseKind, registration.responseType = c.responseKind, c.responseType
	}
	return r.RegisterCommand(registration)
}

func cloneDescriptor(d metadata.Command) metadata.Command {
	if d.BlockOnValidationSeverity != nil {
		value := *d.BlockOnValidationSeverity
		d.BlockOnValidationSeverity = &value
	}
	if d.Authorization != nil {
		a := *d.Authorization
		a.Requirements = slices.Clone(a.Requirements)
		for i := range a.Requirements {
			a.Requirements[i].Roles = slices.Clone(a.Requirements[i].Roles)
			a.Requirements[i].AuthenticationSchemes = slices.Clone(a.Requirements[i].AuthenticationSchemes)
		}
		d.Authorization = &a
	}
	return d
}
func nilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan:
		return v.IsNil()
	}
	return false
}
func taggedKey(t reflect.Type, member string) func(any) (string, bool, error) {
	if member == "" {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields, _ := modelshape.Fields(t) // InspectModel already validated these fields.
	for _, f := range fields {
		if f.Name == member {
			return func(value any) (string, bool, error) {
				return keyText(modelshape.Value(reflect.ValueOf(value), f.Index))
			}
		}
	}
	return nil
}
