// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"errors"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

var (
	// ErrNoResolver identifies operation resources without a DI resolver.
	ErrNoResolver = errors.New("operation scope has no dependency-injection resources")
	// ErrScopeOwner identifies DI resources not owned by the configured provider.
	ErrScopeOwner = errors.New("operation scope resources belong to a different provider")
)

// Resolver returns a guarded, non-closing resolver, never the raw resource holder.
// Each resolution is admitted through Use and checks security/cancellation before
// and after dependency code. A retained callback resolver expires with its view.
// Nil/zero scopes return a resolver failing with ErrInvalidScope; empty/plain
// resources fail with ErrNoResolver. Returned dependencies are borrowed and must
// not outlive the operation. Application use still needs its own concurrency contract.
func (s *Scope) Resolver() di.Resolver { return guardedResolver{scope: s} }

type guardedResolver struct{ scope *Scope }

func (r guardedResolver) Resolve(ctx context.Context, key di.Key) (value any, err error) {
	err = r.scope.Use(ctx, func(ctx context.Context, view *Scope) error {
		resolver, ok := view.Resources().(di.Resolver)
		if !ok {
			return ErrNoResolver
		}
		var resolveErr error
		value, resolveErr = resolver.Resolve(ctx, key)
		return resolveErr
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}

// Resolve resolves the exact registered T through the scope's guarded resolver.
// It validates adapter result types and nil values using Fundamentals contracts.
// No container is required; resources must implement di.Resolver for this path.
func Resolve[T any](ctx context.Context, scope *Scope) (T, error) {
	return di.Resolve[T](ctx, scope.Resolver())
}

// ResourcesFrom adapts a borrowed factory to Arc's operation-opening seam. Each
// call opens a fresh DI scope; Arc owns its Close, never the factory's Close.
// Nil/typed-nil factories fail with ErrInvalidArgument and nil/typed-nil scopes
// with ErrResourceType. ScopeOwner, when implemented, must recognize the scope.
// A scope returned alongside an error remains owned for OpenScope's cleanup.
func ResourcesFrom(factory di.ScopeFactory) OpenResources {
	return func(ctx context.Context) (Resources, error) {
		if ctx == nil || factory == nil || nilResources(factory) {
			return nil, ErrInvalidArgument
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		scope, err := factory.NewScope(ctx)
		if scope == nil || nilResources(scope) {
			return nil, errors.Join(err, ErrResourceType)
		}
		if owner, ok := factory.(di.ScopeOwner); ok && !owner.Owns(scope) {
			return scope, errors.Join(err, ErrScopeOwner)
		}
		return scope, err
	}
}

// CheckOwner admits an ownership check against a configured provider. Call it
// before using borrowed DI resources with that provider. It checks the underlying
// DI scope's identity, not the Arc wrapper or resolver facade. Nil/typed-nil
// owners fail with ErrInvalidArgument; non-DI or foreign resources fail with
// ErrScopeOwner. A factory without the optional ScopeOwner capability cannot
// supply this check; never infer ownership through interface equality.
func (s *Scope) CheckOwner(ctx context.Context, owner di.ScopeOwner) error {
	return s.Use(ctx, func(_ context.Context, view *Scope) error {
		if owner == nil || nilResources(owner) {
			return ErrInvalidArgument
		}
		scope, ok := view.Resources().(di.Scope)
		if !ok || !owner.Owns(scope) {
			return ErrScopeOwner
		}
		return nil
	})
}

// ContextGuard supplies Arc's immutable principal/tenant presence guard for
// container.WithContextGuard. Install it when building the optional Fundamentals
// container to protect factory-internal resolutions as well as Arc entry points.
// Capture/check reject nil contexts and cancellation; correlation and receipt may
// change. Checks support concurrent calls and never retain the captured context.
func ContextGuard() di.CaptureContext {
	return func(ctx context.Context) (di.ContextCheck, error) {
		if ctx == nil {
			return nil, ErrInvalidArgument
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		principal, pp := identity.PrincipalFrom(ctx)
		tenant, tp := tenancy.TenantFrom(ctx)
		return func(ctx context.Context) error {
			if ctx == nil {
				return ErrInvalidArgument
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			currentPrincipal, currentPP := identity.PrincipalFrom(ctx)
			currentTenant, currentTP := tenancy.TenantFrom(ctx)
			if pp != currentPP || tp != currentTP || !principal.Equal(currentPrincipal) || tenant != currentTenant {
				return ErrIdentityChanged
			}
			return nil
		}, nil
	}
}
