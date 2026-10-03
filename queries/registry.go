// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"reflect"

	"github.com/cratis/arc.go/metadata"
)

// RegistryOptions defines logical namespace defaults; import paths are never inferred.
type RegistryOptions struct{ Namespace string }

// Registry is a single-owner builder; zero is usable in the global namespace.
// Successful Build freezes it. Failed Build leaves it editable; pipelines are immutable.
type Registry struct {
	options       RegistryOptions
	frozen        bool
	models        map[reflect.Type]ModelRegistration
	registrations []Registration
	renderers     map[reflect.Type]rendererEntry
	interceptors  []interceptorEntry
	filters       []filterEntry
	guards        []guardEntry
}

// NewRegistry validates namespace configuration without activating dependencies.
func NewRegistry(o RegistryOptions) (*Registry, error) {
	if err := (metadata.Model{Type: metadata.TypeName{Namespace: o.Namespace, Name: "Probe"}}).Validate(); err != nil {
		return nil, err
	}
	return &Registry{options: o}, nil
}

// RegisterReadModel installs exact-type metadata once; method/function query names still must be unique.
func (r *Registry) RegisterReadModel(m ModelRegistration) error {
	if r == nil || m.typ == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	if r.models == nil {
		r.models = map[reflect.Type]ModelRegistration{}
	}
	if _, ok := r.models[m.typ]; ok {
		return ErrDuplicate
	}
	// Reinspect defaults at the actual composition namespace; explicit options win.
	base, err := metadata.InspectModel(m.typ, r.options.Namespace)
	if err != nil {
		return err
	}
	base.Kind = metadata.ReadModel
	c := modelOptions{model: base, seen: map[string]bool{}}
	for _, o := range m.options {
		if err := o.applyModel(&c); err != nil {
			return err
		}
	}
	if err := c.model.Validate(); err != nil {
		return err
	}
	m.model = c.model
	r.models[m.typ] = m
	return nil
}

// RegisterQuery installs a typed adapter. Constructors and callbacks are never run.
func (r *Registry) RegisterQuery(q Registration) error {
	if r == nil || q.invoke == nil || q.bind == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	q.descriptor = copyDescriptor(q.descriptor)
	if !q.descriptorSet {
		model, err := metadata.InspectModel(q.modelType, r.options.Namespace)
		if err != nil {
			return err
		}
		q.descriptor.ReadModel = model.Type
	}
	for _, old := range r.registrations {
		if old.modelType == q.modelType && old.descriptor.Name == q.descriptor.Name || old.descriptor.Identity() == q.descriptor.Identity() {
			return &RegistrationError{q.descriptor.Identity(), ErrDuplicate}
		}
	}
	if err := r.validateDescriptor(q.descriptor); err != nil {
		return err
	}
	r.registrations = append(r.registrations, q)
	return nil
}
func (r *Registry) validateDescriptor(d metadata.Query) error {
	// The existing resolver remains the sole route-validation algorithm.
	_, err := metadata.Resolve(metadata.Catalog{Version: metadata.Version, Queries: []metadata.Query{d}}, metadata.DefaultOptions())
	return err
}
func (r *Registry) materialize(q Registration) Registration {
	if m, ok := r.models[q.modelType]; ok && !q.descriptorSet {
		q.descriptor.ReadModel = m.model.Type
		q.descriptor.ReadModelPath = m.model.Path
		q.descriptor.ReadModelAuthorization = copyAuthorization(m.model.Authorization)
		q.descriptor.ReadModelIdentityMember = m.model.IdentityMember
		q.descriptor.ExcludeFromDiscovery = q.descriptor.ExcludeFromDiscovery || m.model.ExcludeFromDiscovery
	}
	return q
}

// Catalog returns a mutable copy, including excluded queries for authorization.
func (r *Registry) Catalog() metadata.Catalog {
	c := metadata.Catalog{Version: metadata.Version}
	if r == nil {
		return c
	}
	for _, q := range r.registrations {
		c.Queries = append(c.Queries, r.materialize(q).Descriptor())
	}
	return c
}
