// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"reflect"
	"slices"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/serialization"
)

// ModelOption configures one owning read model. Repeated singular options fail.
type ModelOption interface{ applyModel(*modelOptions) error }
type modelOption struct {
	name  string
	apply func(*metadata.Model)
}
type modelOptions struct {
	model metadata.Model
	seen  map[string]bool
}

func (o modelOption) applyModel(c *modelOptions) error {
	if c.seen[o.name] {
		return ErrDuplicate
	}
	c.seen[o.name] = true
	o.apply(&c.model)
	return nil
}

// WithModelIdentity pins a logical identity independent of package/type relocation.
func WithModelIdentity(t metadata.TypeName) ModelOption {
	return modelOption{"identity", func(m *metadata.Model) { m.Type = t }}
}

// WithModelName overrides the model name while retaining namespace defaults.
func WithModelName(name string) ModelOption {
	return modelOption{"name", func(m *metadata.Model) { m.Type.Name = name }}
}

// WithModelNamespace overrides the namespace without changing the model name.
func WithModelNamespace(namespace string) ModelOption {
	return modelOption{"namespace", func(m *metadata.Model) { m.Type.Namespace = namespace }}
}

// WithModelExcludeFromDiscovery hides all queries on the owning read model.
func WithModelExcludeFromDiscovery(exclude bool) ModelOption {
	return modelOption{"exclude", func(m *metadata.Model) { m.ExcludeFromDiscovery = exclude }}
}

// WithModelPath sets the literal model-level route override.
func WithModelPath(path string) ModelOption {
	return modelOption{"path", func(m *metadata.Model) { m.Path = path }}
}

// WithModelAuthorization sets the model's fallback declaration for its queries.
func WithModelAuthorization(a metadata.Authorization) ModelOption {
	a = *copyAuthorization(&a)
	return modelOption{"authorization", func(m *metadata.Model) { m.Authorization = copyAuthorization(&a) }}
}

// ModelRegistration is immutable model metadata. Construct using RegisterReadModel.
type ModelRegistration struct {
	typ     reflect.Type
	model   metadata.Model
	options []ModelOption
}

// ModelType returns the exact nonpointer owning struct.
func (m ModelRegistration) ModelType() reflect.Type { return m.typ }

// Descriptor returns independently owned model declaration metadata.
func (m ModelRegistration) Descriptor() metadata.Model {
	m.model.Authorization = copyAuthorization(m.model.Authorization)
	return m.model
}

// RegisterReadModel declares metadata once. Registration is optional when defaults suffice.
// M must be a named nonpointer struct; Chronicle registrations remain independent.
func RegisterReadModel[M any](r Registrar, options ...ModelOption) error {
	if nilValue(r) {
		return ErrInvalidRegistration
	}
	t := reflect.TypeFor[M]()
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return ErrInvalidRegistration
	}
	m, err := metadata.InspectModel(t, "")
	if err != nil {
		return err
	}
	if m.Kind != "" && m.Kind != metadata.ReadModel {
		return ErrInvalidRegistration
	}
	m.Kind = metadata.ReadModel
	c := modelOptions{model: m, seen: map[string]bool{}}
	for _, o := range options {
		if o == nil {
			return ErrInvalidRegistration
		}
		if err := o.applyModel(&c); err != nil {
			return err
		}
	}
	if err := c.model.Validate(); err != nil {
		return err
	}
	if err := serialization.ValidateType(t); err != nil {
		return err
	}
	return r.RegisterReadModel(ModelRegistration{typ: t, model: c.model, options: slices.Clone(options)})
}
func copyAuthorization(a *metadata.Authorization) *metadata.Authorization {
	if a == nil {
		return nil
	}
	c := *a
	c.Requirements = slices.Clone(a.Requirements)
	for i := range c.Requirements {
		c.Requirements[i].Roles = slices.Clone(c.Requirements[i].Roles)
		c.Requirements[i].AuthenticationSchemes = slices.Clone(c.Requirements[i].AuthenticationSchemes)
	}
	return &c
}
func copyDescriptor(d metadata.Query) metadata.Query {
	d.Authorization = copyAuthorization(d.Authorization)
	d.ReadModelAuthorization = copyAuthorization(d.ReadModelAuthorization)
	if d.Path != nil {
		p := *d.Path
		d.Path = &p
	}
	return d
}
func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return r.IsNil()
	}
	return false
}
