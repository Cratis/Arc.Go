// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"net/http"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// UsersProvider supplies display-only development users in provider order.
type UsersProvider interface {
	ProvideUsers(context.Context) ([]identity.User, error)
}

// TenantsProvider supplies display-only development tenants in provider order.
type TenantsProvider interface {
	ProvideTenants(context.Context) ([]tenancy.Tenant, error)
}

type listProvider[T any] struct {
	name    string
	factory Factory[T]
	keys    []di.Key
}

// AddUsersProvider registers a lazy resource-scoped discovery provider.
func (b *Builder) AddUsersProvider(name string, factory Factory[UsersProvider], keys ...di.Key) error {
	if b.attempted {
		return ErrFrozen
	}
	if !providerName(name) || factory == nil {
		return ErrInvalidOptions
	}
	for _, p := range b.users {
		if p.name == name {
			return ErrInvalidOptions
		}
	}
	b.users = append(b.users, listProvider[UsersProvider]{name, factory, append([]di.Key(nil), keys...)})
	return nil
}

// AddTenantsProvider registers a lazy resource-scoped discovery provider.
func (b *Builder) AddTenantsProvider(name string, factory Factory[TenantsProvider], keys ...di.Key) error {
	if b.attempted {
		return ErrFrozen
	}
	if !providerName(name) || factory == nil {
		return ErrInvalidOptions
	}
	for _, p := range b.tenants {
		if p.name == name {
			return ErrInvalidOptions
		}
	}
	b.tenants = append(b.tenants, listProvider[TenantsProvider]{name, factory, append([]di.Key(nil), keys...)})
	return nil
}
func (a *Application) discoveryEndpoint(w http.ResponseWriter, r *http.Request) {
	principal, _ := identity.PrincipalFrom(r.Context())
	if a.discovery == discoveryAuthenticated {
		if !principal.IsAuthenticated() {
			w.WriteHeader(401)
			return
		}
		if len(a.options.Introspection.Roles) > 0 {
			allowed := false
			for _, role := range a.options.Introspection.Roles {
				allowed = allowed || principal.HasRole(role)
			}
			if !allowed {
				w.WriteHeader(403)
				return
			}
		}
	}
	if body, ok := a.catalogJSON[r.URL.Path]; ok {
		a.publish(w, r, 200, body)
		return
	}
	var value any
	var err error
	switch r.URL.Path {
	case "/.cratis/users":
		value = []identity.User{}
		if len(a.users) > 0 {
			value, err = a.withResources(r.Context(), func(ctx context.Context, s *execution.Scope) (any, error) {
				users := []identity.User{}
				for _, p := range a.users {
					provider, err := p.factory(ctx, s)
					if err != nil {
						return nil, err
					}
					if nilValue(provider) {
						return nil, ErrInvalidOptions
					}
					entries, err := provider.ProvideUsers(ctx)
					if err != nil {
						return nil, err
					}
					users = append(users, entries...)
				}
				return users, nil
			})
		}
	case "/.cratis/tenants":
		value = []tenancy.Tenant{}
		if len(a.tenants) > 0 {
			value, err = a.withResources(r.Context(), func(ctx context.Context, s *execution.Scope) (any, error) {
				tenants := []tenancy.Tenant{}
				for _, p := range a.tenants {
					provider, err := p.factory(ctx, s)
					if err != nil {
						return nil, err
					}
					if nilValue(provider) {
						return nil, ErrInvalidOptions
					}
					entries, err := provider.ProvideTenants(ctx)
					if err != nil {
						return nil, err
					}
					tenants = append(tenants, entries...)
				}
				return tenants, nil
			})
		}
	default:
		w.WriteHeader(404)
		return
	}
	if err != nil {
		a.hostFailure(r.Context(), "Discovery provider failed", err)
		w.WriteHeader(500)
		return
	}
	a.publish(w, r, 200, value)
}
