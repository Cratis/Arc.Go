// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"reflect"
	"strings"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Factory constructs a stage-local borrowed provider under an expiring scope view.
// Resource ownership stays with the configured operation resource opener.
type Factory[T any] func(context.Context, *execution.Scope) (T, error)

type detailsRegistration struct {
	name    string
	typ     reflect.Type
	scoped  bool
	keys    []di.Key
	provide func(context.Context, *execution.Scope) (any, error)
}

// RegisterIdentityDetails registers a borrowed, concurrently callable provider.
func RegisterIdentityDetails[T any](b *Builder, name string, provider identity.DetailsProvider[T]) error {
	if nilValue(provider) {
		return identity.ErrInvalidProvider
	}
	return registerDetails(b, detailsRegistration{name: name, typ: reflect.TypeFor[T](), provide: func(ctx context.Context, _ *execution.Scope) (any, error) {
		return identity.ProvideDetails(ctx, provider)
	}})
}

// RegisterScopedIdentityDetails registers a lazy request-scoped details factory.
// Declared dependency keys are checked at Build without resolving them.
func RegisterScopedIdentityDetails[T any](b *Builder, name string, factory Factory[identity.DetailsProvider[T]], dependencies ...di.Key) error {
	if factory == nil {
		return identity.ErrInvalidProvider
	}
	return registerDetails(b, detailsRegistration{name: name, typ: reflect.TypeFor[T](), scoped: true, keys: append([]di.Key(nil), dependencies...), provide: func(ctx context.Context, s *execution.Scope) (any, error) {
		provider, err := factory(ctx, s)
		if err != nil {
			return nil, err
		}
		return identity.ProvideDetails(ctx, provider)
	}})
}
func registerDetails(b *Builder, r detailsRegistration) error {
	if b == nil || !providerName(r.name) {
		return ErrInvalidOptions
	}
	if b.attempted {
		return ErrFrozen
	}
	for _, old := range b.details {
		if old.name == r.name {
			return ErrInvalidOptions
		}
	}
	b.details = append(b.details, r)
	return nil
}
func providerName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "\r\n\x00")
}
func (b *Builder) selectDetails() (detailsRegistration, error) {
	selected := b.options.Identity.DetailsProvider
	if selected == "" && len(b.details) > 1 {
		return detailsRegistration{}, ErrInvalidOptions
	}
	for _, r := range b.details {
		if selected == "" || selected == r.name {
			if err := checkProviderKeys(b.options.DependencyCatalog, r.keys); err != nil {
				return r, err
			}
			return r, nil
		}
	}
	if selected != "" {
		return detailsRegistration{}, ErrInvalidOptions
	}
	return detailsRegistration{typ: reflect.TypeFor[struct{}](), provide: func(ctx context.Context, _ *execution.Scope) (any, error) {
		return identity.ProvideDetails(ctx, identity.DetailsProviderFunc[struct{}](func(context.Context, identity.Context) (identity.Details[struct{}], error) {
			return identity.Details[struct{}]{IsUserAuthorized: true}, nil
		}))
	}}, nil
}
func checkProviderKeys(catalog di.Catalog, keys []di.Key) error {
	for _, key := range keys {
		if key.Type() == nil || nilValue(catalog) || !catalog.Contains(key) {
			return ErrInvalidOptions
		}
	}
	return nil
}
