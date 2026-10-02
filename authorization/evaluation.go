// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization

import (
	"context"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

// Evaluator is immutable and concurrent-safe with concurrently callable policies.
// It caches declarations, never decisions. Construct using Registry.Build.
type Evaluator struct{ declarations map[Target]declaration }

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
	principal := p.principal
	if !principal.IsAuthenticated() {
		// Policies see a synthetic guest; Check still certifies the original caller.
		principal = identity.Principal{}
	}
	receivedAt, _ := execution.ReceivedAt(ctx)
	value := Context{Principal: principal, Tenant: p.tenant, Target: p.target, Resource: resource, ReceivedAt: receivedAt}
	for _, requirement := range declaration.requirements {
		policy := requirement.registration.policy
		if policy == nil {
			continue
		}
		if err := p.Check(ctx); err != nil {
			return Decision{}, err
		}
		decision, err := policy.Authorize(ctx, value)
		if check := p.Check(ctx); check != nil {
			return Decision{}, check
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
