// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"reflect"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Factory constructs a borrowed stage-local extension. Resources own disposal.
type Factory[T any] func(context.Context, *execution.Scope) (T, error)

// Option is a sealed, typed registration option. Singular options cannot repeat.
type Option[C any] interface{ apply(*configuration[C]) error }

type configuration[C any] struct {
	descriptor       metadata.Command
	handler          *adapter
	seen             map[string]bool
	validators       []validatorEntry
	withoutModel     bool
	key              func(C) (string, bool)
	dependencies     []di.Key
	responseKind     ResponseKind
	responseType     reflect.Type
	responseOverride bool
}
type option[C any] struct {
	name string
	set  func(*configuration[C]) error
}

func (o option[C]) apply(c *configuration[C]) error {
	if o.name != "" {
		if c.seen[o.name] {
			return ErrDuplicate
		}
		c.seen[o.name] = true
	}
	return o.set(c)
}

// WithDescriptor pins the complete descriptor before individual overrides.
func WithDescriptor[C any](d metadata.Command) Option[C] {
	d = cloneDescriptor(d)
	return option[C]{"descriptor", func(c *configuration[C]) error { c.descriptor = cloneDescriptor(d); return nil }}
}

// WithName overrides the logical command name while retaining namespace defaults.
func WithName[C any](name string) Option[C] {
	return option[C]{"name", func(c *configuration[C]) error { c.descriptor.Type.Name = name; return nil }}
}

// WithExcludeFromDiscovery hides discovery without changing routes or authorization.
func WithExcludeFromDiscovery[C any](exclude bool) Option[C] {
	return option[C]{"exclude", func(c *configuration[C]) error { c.descriptor.ExcludeFromDiscovery = exclude; return nil }}
}

// WithNamespace overrides the public namespace, never a Go import path.
func WithNamespace[C any](s string) Option[C] {
	return option[C]{"namespace", func(c *configuration[C]) error { c.descriptor.Type.Namespace = s; return nil }}
}

// WithPath overrides the entire route.
func WithPath[C any](s string) Option[C] {
	return option[C]{"path", func(c *configuration[C]) error { c.descriptor.Path = s; return nil }}
}

// WithAuthorization replaces the declaration rather than merging it.
func WithAuthorization[C any](a metadata.Authorization) Option[C] {
	d := cloneDescriptor(metadata.Command{Authorization: &a})
	return option[C]{"authorization", func(c *configuration[C]) error {
		c.descriptor.Authorization = cloneDescriptor(d).Authorization
		return nil
	}}
}

// WithBlockOnValidationSeverity supplies an inclusive, non-loosenable floor.
func WithBlockOnValidationSeverity[C any](s validation.Severity) Option[C] {
	return option[C]{"severity", func(c *configuration[C]) error { c.descriptor.BlockOnValidationSeverity = &s; return nil }}
}

// WithKey supplies an explicit fallback command key selector.
func WithKey[C any](key func(C) (string, bool)) Option[C] {
	return option[C]{"key", func(c *configuration[C]) error {
		if key == nil {
			return ErrInvalidRegistration
		}
		c.key = key
		return nil
	}}
}

// WithValidator appends an ordered, shared validator.
func WithValidator[C any](v validation.Validator[C]) Option[C] {
	return option[C]{set: func(c *configuration[C]) error {
		if nilValue(v) {
			return ErrInvalidRegistration
		}
		c.validators = append(c.validators, validatorEntry{invoke: func(ctx context.Context, _ *execution.Scope, value any) ([]validation.Result, error) {
			return validation.Invoke(ctx, v, value.(C))
		}})
		return nil
	}}
}

// WithScopedValidator constructs a validator only in validation, after authorization.
func WithScopedValidator[C any](factory Factory[validation.Validator[C]], keys ...di.Key) Option[C] {
	keys = append([]di.Key(nil), keys...)
	return option[C]{set: func(c *configuration[C]) error {
		if factory == nil {
			return ErrInvalidRegistration
		}
		c.dependencies = append(c.dependencies, keys...)
		c.validators = append(c.validators, validatorEntry{invoke: func(ctx context.Context, scope *execution.Scope, value any) (results []validation.Result, err error) {
			v, err := factory(ctx, scope)
			if err != nil {
				return nil, err
			}
			if nilValue(v) {
				return nil, ErrInvalidRegistration
			}
			return validation.Invoke(ctx, v, value.(C))
		}})
		return nil
	}}
}

// WithoutModelValidation explicitly disables graph methods, registered rules and tags.
func WithoutModelValidation[C any]() Option[C] {
	return option[C]{"model-validation", func(c *configuration[C]) error { c.withoutModel = true; return nil }}
}

// WithPreparationDependencies declares keys for static catalog checks, not resolution.
func WithPreparationDependencies[C any](keys ...di.Key) Option[C] {
	return dependencyOption[C]("preparation-dependencies", keys)
}

// WithHandlingDependencies declares Handle-only DI keys without eager activation.
func WithHandlingDependencies[C any](keys ...di.Key) Option[C] {
	return dependencyOption[C]("handling-dependencies", keys)
}
func dependencyOption[C any](name string, keys []di.Key) Option[C] {
	keys = append([]di.Key(nil), keys...)
	return option[C]{name, func(c *configuration[C]) error { c.dependencies = append(c.dependencies, keys...); return nil }}
}

// WithResponseType declares an enforced client response contract.
func WithResponseType[C, R any]() Option[C] {
	return option[C]{"response", func(c *configuration[C]) error {
		c.responseOverride = true
		c.responseKind = ResponseValue
		c.responseType = reflect.TypeFor[R]()
		return nil
	}}
}

// WithNoResponse requires every returned value to be consumed.
func WithNoResponse[C any]() Option[C] {
	return option[C]{"response", func(c *configuration[C]) error { c.responseOverride = true; c.responseKind = ResponseNone; return nil }}
}
