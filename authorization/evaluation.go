// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization

import (
	"context"
	"errors"
	"slices"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/tenancy"
)

// Evaluator is immutable and concurrent-safe with concurrently callable policies.
// It caches declarations, never decisions. Construct using Registry.Build.
type Evaluator struct {
	declarations map[Target]declaration
	sources      map[Target][]*metadata.Authorization
}

// ErrScopeRequired identifies direct evaluation of an applicable scoped policy.
var ErrScopeRequired = errors.New("authorization policy requires an operation scope")

// ErrCatalogMismatch identifies missing targets or changed frozen declarations.
var ErrCatalogMismatch = errors.New("authorization catalog mismatch")

// CheckCatalog verifies coverage and exact declaration content, including absent
// declarations and overridden model levels. Supersets are allowed. It does not
// run policies, and never treats matching names alone as compatible authority.
func (e *Evaluator) CheckCatalog(catalog metadata.Catalog) error {
	if e == nil || catalog.Version != metadata.Version {
		return ErrCatalogMismatch
	}
	seen := make(map[Target]bool)
	check := func(target Target, levels ...*metadata.Authorization) error {
		if seen[target] {
			return ErrCatalogMismatch
		}
		seen[target] = true
		frozen, exists := e.sources[target]
		if !exists || len(frozen) != len(levels) {
			return ErrCatalogMismatch
		}
		for i, level := range levels {
			if !sameAuthorization(frozen[i], level) {
				return ErrCatalogMismatch
			}
		}
		return nil
	}
	for _, command := range catalog.Commands {
		if !validType(command.Type) {
			return ErrCatalogMismatch
		}
		if err := check(Target{Kind: Command, Identity: command.Type.Identity()}, command.Authorization); err != nil {
			return err
		}
	}
	for _, query := range catalog.Queries {
		if !validType(query.ReadModel) || !validSegment(query.Name) {
			return ErrCatalogMismatch
		}
		if err := check(Target{Kind: Query, Identity: query.Identity()}, query.Authorization, query.ReadModelAuthorization); err != nil {
			return err
		}
	}
	return nil
}

func cloneAuthorization(value *metadata.Authorization) *metadata.Authorization {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Requirements = slices.Clone(value.Requirements)
	for i := range copy.Requirements {
		copy.Requirements[i].Roles = slices.Clone(copy.Requirements[i].Roles)
		copy.Requirements[i].AuthenticationSchemes = slices.Clone(copy.Requirements[i].AuthenticationSchemes)
	}
	return &copy
}

// Prepared captures a declaration and security metadata before dependencies run.
// Zero is invalid. It is not an authorization token; Evaluate is required for
// every operation, and Check must run again immediately before handler invocation.
type Prepared struct {
	evaluator        *Evaluator
	target           Target
	declaration      declaration
	principal        identity.Principal
	tenant           tenancy.ID
	principalPresent bool
	tenantPresent    bool
}

// Prepare captures metadata without invoking policies or constructing dependencies.
func (e *Evaluator) Prepare(ctx context.Context, target Target) (Prepared, error) {
	if ctx == nil {
		return Prepared{}, ErrNotPrepared
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if e == nil {
		return Prepared{}, ErrUnknownTarget
	}
	declaration, ok := e.declarations[target]
	if !ok {
		return Prepared{}, ErrUnknownTarget
	}
	principal, pp := identity.PrincipalFrom(ctx)
	tenant, tp := tenancy.TenantFrom(ctx)
	return Prepared{evaluator: e, target: target, declaration: declaration, principal: principal, tenant: tenant, principalPresent: pp, tenantPresent: tp}, nil
}

// Check verifies cancellation and unchanged principal/tenant, including presence.
// Receipt/correlation changes do not alter security ownership.
func (p Prepared) Check(ctx context.Context) error {
	if p.evaluator == nil || ctx == nil {
		return ErrNotPrepared
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	principal, pp := identity.PrincipalFrom(ctx)
	tenant, tp := tenancy.TenantFrom(ctx)
	if pp != p.principalPresent || tp != p.tenantPresent || !p.principal.Equal(principal) || p.tenant != tenant {
		return ErrIdentityChanged
	}
	return nil
}

// Evaluate checks security metadata, roles, then direct policies in declaration
// order. A policy error returns a denied Decision plus its original error. Policy
// panics propagate to pipeline boundaries. Resource is borrowed, never retained.
// This API is container-neutral; it neither creates nor validates service scopes.
func (p Prepared) Evaluate(ctx context.Context, resource any) (Decision, error) {
	return p.evaluate(ctx, nil, resource)
}

// EvaluateScoped admits evaluation for the scope's lifetime. All authentication
// and role requirements run before any policy factory. Factories and policies
// receive only a non-closing view; panics become execution.PanicError. A scope
// grants no permission and decisions are never cached.
func (p Prepared) EvaluateScoped(ctx context.Context, scope *execution.Scope, resource any) (decision Decision, err error) {
	err = scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
		var evaluateErr error
		decision, evaluateErr = p.evaluate(ctx, view, resource)
		return evaluateErr
	})
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func (p Prepared) evaluate(ctx context.Context, scope *execution.Scope, resource any) (Decision, error) {
	if err := p.Check(ctx); err != nil {
		return Decision{}, err
	}
	declaration := p.declaration
	if declaration.public {
		return Allow(), nil
	}
	if !p.principal.IsAuthenticated() && !declaration.guest {
		return Deny("authentication required"), nil
	}
	for _, requirement := range declaration.requirements {
		if len(requirement.roles) == 0 {
			continue
		}
		allowed := false
		for _, role := range requirement.roles {
			if p.principal.HasRole(role) {
				allowed = true
				break
			}
		}
		if !allowed {
			return Deny("role required"), nil
		}
	}
	if scope == nil {
		for _, requirement := range declaration.requirements {
			if requirement.registration.factory != nil {
				return Decision{}, ErrScopeRequired
			}
		}
	}
	principal := p.principal
	if !principal.IsAuthenticated() {
		// Policies see a synthetic guest; Check still certifies the original caller.
		principal = identity.Principal{}
	}
	receivedAt, _ := execution.ReceivedAt(ctx)
	value := Context{Principal: principal, Tenant: p.tenant, Target: p.target, Resource: resource, ReceivedAt: receivedAt}
	for _, requirement := range declaration.requirements {
		if err := p.Check(ctx); err != nil {
			return Decision{}, err
		}
		if scope != nil {
			if err := scope.CheckContext(ctx); err != nil {
				return Decision{}, err
			}
		}
		policy := requirement.registration.policy
		if factory := requirement.registration.factory; factory != nil {
			var err error
			policy, err = factory(ctx, scope)
			if check := p.Check(ctx); check != nil {
				return Decision{}, errors.Join(err, check)
			}
			if err != nil {
				return Decision{}, err
			}
		}
		if policy == nil {
			continue
		}
		decision, err := policy.Authorize(ctx, value)
		if scope != nil {
			if check := scope.CheckContext(ctx); check != nil {
				return Decision{}, errors.Join(err, check)
			}
		}
		if check := p.Check(ctx); check != nil {
			if err == nil {
				return Decision{}, check
			}
			return Decision{}, errors.Join(err, check)
		}
		if err != nil {
			return Decision{}, err
		}
		if !decision.IsAllowed() {
			return decision, nil
		}
	}
	if err := p.Check(ctx); err != nil {
		return Decision{}, err
	}
	return Allow(), nil
}
