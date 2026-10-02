// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"errors"
	"reflect"
	"strings"
	"unicode"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// RegistryOptions configures public identity defaults.
type RegistryOptions struct{ Namespace string }

// Registry is a single-owner builder. Zero uses the global namespace. Successful
// Build freezes it; failed Build leaves it editable. It never activates factories.
type Registry struct {
	namespace     string
	registrations []Registration
	frozen        bool
	names         map[string]bool
	providers     []extension[ContextValuesProvider]
	keys          []extension[KeyResolver]
	filters       []extension[Filter]
	authFilters   []extension[AuthorizationFilter]
	responses     []extension[ResponseValueHandler]
	participants  []extension[ExecutionScope]
}
type extension[T any] struct {
	name    string
	factory Factory[T]
	keys    []di.Key
}

// NewRegistry validates namespace configuration without application callbacks.
func NewRegistry(options RegistryOptions) (*Registry, error) {
	if err := (metadata.Model{Type: metadata.TypeName{Namespace: options.Namespace, Name: "Command"}}).Validate(); err != nil {
		return nil, errors.Join(ErrInvalidRegistration, err)
	}
	return &Registry{namespace: options.Namespace}, nil
}
func (r *Registry) commandNamespace() string {
	if r == nil {
		return ""
	}
	return r.namespace
}

// RegisterCommand accepts only valid immutable registrations and exact ownership.
func (r *Registry) RegisterCommand(value Registration) error {
	if r == nil || value.commandType == nil || !value.adapter.valid {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	for _, existing := range r.registrations {
		if existing.commandType == value.commandType || existing.descriptor.Type.Identity() == value.descriptor.Type.Identity() {
			return &RegistrationError{value.descriptor.Type.Identity(), ErrDuplicate}
		}
	}
	r.registrations = append(r.registrations, value)
	return nil
}

// Catalog returns copy-isolated declaration metadata in registration order.
func (r *Registry) Catalog() metadata.Catalog {
	catalog := metadata.Catalog{Version: metadata.Version}
	if r != nil {
		for _, entry := range r.registrations {
			catalog.Commands = append(catalog.Commands, entry.Descriptor())
		}
	}
	return catalog
}

// Build validates catalogs and shapes, without resolving services, and freezes
// the registry only on success. Returned pipelines are concurrently callable.
func (r *Registry) Build(options PipelineOptions) (Pipeline, error) {
	if r == nil {
		return nil, ErrInvalidRegistration
	}
	if r.frozen {
		return nil, ErrFrozen
	}
	if options.CleanupTimeout < 0 || (options.OpenResources != nil && options.ScopeFactory != nil) || (options.ScopeFactory != nil && nilValue(options.ScopeFactory)) || (options.Membership != nil && nilValue(options.Membership)) {
		return nil, ErrInvalidRegistration
	}
	catalog := options.DependencyCatalog
	if options.ScopeFactory != nil {
		options.OpenResources = execution.ResourcesFrom(options.ScopeFactory)
		if catalog == nil {
			catalog, _ = options.ScopeFactory.(di.Catalog)
		}
	}
	checkKeys := func(keys []di.Key) error {
		for _, key := range keys {
			if key.Type() == nil || nilValue(catalog) || !catalog.Contains(key) {
				return ErrInvalidRegistration
			}
		}
		return nil
	}
	for _, registration := range r.registrations {
		if err := checkKeys(registration.dependencies); err != nil {
			return nil, &RegistrationError{registration.descriptor.Type.Identity(), err}
		}
		if !registration.withoutModel {
			if err := options.Validation.CheckType(registration.commandType); err != nil {
				return nil, err
			}
		}
	}
	for _, keys := range r.extensionKeys() {
		if err := checkKeys(keys); err != nil {
			return nil, err
		}
	}
	if options.Authorization == nil {
		var auth authorization.Registry
		evaluator, err := auth.Build(r.Catalog(), authorization.Options{})
		if err != nil {
			return nil, err
		}
		options.Authorization = evaluator
	} else if err := options.Authorization.CheckCatalog(r.Catalog()); err != nil {
		return nil, err
	}
	// Reuse metadata's existing route/collision rules; no second route algorithm.
	if _, err := metadata.Resolve(r.Catalog(), metadata.DefaultOptions()); err != nil {
		return nil, err
	}
	p := &pipeline{options: options, byType: make(map[reflect.Type]Registration), byName: make(map[string]Registration), providers: append([]extension[ContextValuesProvider](nil), r.providers...), keys: append([]extension[KeyResolver](nil), r.keys...), filters: append([]extension[Filter](nil), r.filters...), authFilters: append([]extension[AuthorizationFilter](nil), r.authFilters...), responses: append([]extension[ResponseValueHandler](nil), r.responses...), participants: append([]extension[ExecutionScope](nil), r.participants...)}
	for _, entry := range r.registrations {
		if entry.responseKind == ResponseUnknown && len(r.responses) == 0 && entry.adapter.returnType.Kind() != reflect.Interface {
			entry.responseKind, entry.responseType = ResponseValue, entry.adapter.returnType
		}
		p.byType[entry.commandType], p.byName[entry.descriptor.Type.Identity()] = entry, entry
	}
	r.frozen = true
	return p, nil
}
func addExtension[T any](r *Registry, kind, name string, factory Factory[T], keys []di.Key, entries *[]extension[T]) error {
	if r == nil || factory == nil || !validExtensionName(name) {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	if r.names == nil {
		r.names = make(map[string]bool)
	}
	identity := kind + ":" + name
	if r.names[identity] {
		return ErrDuplicate
	}
	r.names[identity] = true
	*entries = append(*entries, extension[T]{name, factory, append([]di.Key(nil), keys...)})
	return nil
}
func validExtensionName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func (r *Registry) extensionKeys() [][]di.Key {
	var result [][]di.Key
	for _, e := range r.providers {
		result = append(result, e.keys)
	}
	for _, e := range r.keys {
		result = append(result, e.keys)
	}
	for _, e := range r.filters {
		result = append(result, e.keys)
	}
	for _, e := range r.authFilters {
		result = append(result, e.keys)
	}
	for _, e := range r.responses {
		result = append(result, e.keys)
	}
	for _, e := range r.participants {
		result = append(result, e.keys)
	}
	return result
}
