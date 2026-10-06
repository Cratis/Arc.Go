// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"reflect"
	"strings"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/validation"
)

// ReadModelOwnership controls exact-type provider precedence independently of
// registration order. Providers at the same level cannot claim the same model.
type ReadModelOwnership uint8

const (
	// FallbackReadModel supplies models with no declared owner or application override.
	FallbackReadModel ReadModelOwnership = iota
	// DeclaredReadModel claims models backed by the provider's declared producer.
	DeclaredReadModel
	// OverrideReadModel is an explicit application replacement, above declared ownership.
	OverrideReadModel
)

// ReadModelInstance preserves absence independently of a zero-valued model.
// Value is borrowed from the provider for this frame. Providers own materialization,
// release, caching and mutable-value isolation; a release failure must be an error.
type ReadModelInstance[M any] struct {
	// Value is meaningful only when Exists is true.
	Value M
	// Exists distinguishes an absent model from a present zero value.
	Exists bool
}

type readModelValue struct {
	value  any
	exists bool
}

type readModelProvider struct {
	name      string
	ownership ReadModelOwnership
	resolve   func(context.Context, *Invocation, string) (readModelValue, error)
}

// RegisterReadModelProvider binds the exact non-pointer struct M for command
// dependencies, not public queries. Declared owners beat fallbacks; explicit
// overrides beat both. Duplicate claims at any one level fail even if overridden.
// The resolver is borrowed and must support concurrent independent executions.
// It may use FrameState for provider-specific caches; the core does not cache or
// infer coordinates/read modes. ResolveReadModel always validates the command key.
func RegisterReadModelProvider[M any](r *Registry, name string, ownership ReadModelOwnership, resolve func(context.Context, *Invocation, string) (ReadModelInstance[M], error)) error {
	model := reflect.TypeFor[M]()
	if r == nil || !validExtensionName(name) || ownership > OverrideReadModel || resolve == nil || model.Kind() != reflect.Struct {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	for _, existing := range r.models[model] {
		if existing.ownership == ownership {
			return ErrDuplicate
		}
	}
	if r.models == nil {
		r.models = make(map[reflect.Type][]readModelProvider)
	}
	r.models[model] = append(r.models[model], readModelProvider{name: name, ownership: ownership, resolve: func(ctx context.Context, inv *Invocation, key string) (readModelValue, error) {
		instance, err := resolve(ctx, inv, key)
		return readModelValue{value: instance.Value, exists: instance.Exists}, err
	}})
	return nil
}

func (r *Registry) readModelProviders() map[reflect.Type]readModelProvider {
	result := make(map[reflect.Type]readModelProvider, len(r.models))
	for model, providers := range r.models {
		selected := providers[0]
		for _, provider := range providers[1:] {
			if provider.ownership > selected.ownership {
				selected = provider
			}
		}
		result[model] = selected
	}
	return result
}

// ResolveReadModel reads M through its selected owner with presence intact.
// Missing/blank keys are malformedRequest, even for optional reads. A missing
// provider is ErrReadModelProvider, not absence. Reader/release errors remain
// inspectable and are never cached or converted into a missing instance.
func ResolveReadModel[M any](ctx context.Context, inv *Invocation) (ReadModelInstance[M], error) {
	var result ReadModelInstance[M]
	var provider readModelProvider
	err := withState(ctx, inv, func(e *Execution) error {
		var ok bool
		provider, ok = e.frame.pipeline.models[reflect.TypeFor[M]()]
		if !ok {
			return ErrReadModelProvider
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	key, present := inv.CommandContext().ResolvedKey()
	if !present || strings.TrimSpace(key) == "" {
		return result, validation.Reject(validation.Result{Severity: validation.Error, Reason: validation.MalformedRequest, Message: "A read-model key is required."})
	}
	err = boundary.Call(ctx, func(ctx context.Context) error {
		value, err := provider.resolve(ctx, inv, key)
		if err != nil {
			return err
		}
		if value.exists {
			result.Value, result.Exists = value.value.(M), true
		}
		return inv.owner.Check(ctx)
	})
	if err != nil {
		return ReadModelInstance[M]{}, err
	}
	return result, nil
}

// RequireReadModel resolves M and reports absent instances as dependencyUnavailable.
// It never manufactures a successful zero-valued dependency after a failed read.
func RequireReadModel[M any](ctx context.Context, inv *Invocation) (M, error) {
	instance, err := ResolveReadModel[M](ctx, inv)
	if err == nil && !instance.Exists {
		err = validation.Reject(validation.Result{Severity: validation.Error, Reason: validation.DependencyUnavailable, Message: "The required read model is unavailable."})
	}
	return instance.Value, err
}

// ReadModelOrNil returns nil only for a valid key with no instance. The returned
// pointer owns a shallow value copy; reference-valued model fields remain borrowed.
func ReadModelOrNil[M any](ctx context.Context, inv *Invocation) (*M, error) {
	instance, err := ResolveReadModel[M](ctx, inv)
	if err != nil || !instance.Exists {
		return nil, err
	}
	return &instance.Value, nil
}
