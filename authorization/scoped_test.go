// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
)

func TestScopedPoliciesAreLazyAndAllRolesPrecedeFactories(t *testing.T) {
	var registry authorization.Registry
	calls := 0
	var retained *execution.Scope
	err := authorization.RegisterPolicy(&registry, "scoped", func(ctx context.Context, scope *execution.Scope) (authorization.PolicyFunc, error) {
		calls++
		retained = scope
		if !errors.Is(scope.Close(ctx), execution.ErrScopeView) {
			t.Error("owning policy scope")
		}
		return func(_ context.Context, value authorization.Context) (authorization.Decision, error) {
			if value.Resource != "input" {
				t.Error("missing resource")
			}
			return authorization.Allow(), nil
		}, nil
	}, authorization.PolicyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	declaration := &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "scoped"}, {Roles: []string{"Editor"}}}}
	e := build(t, &registry, catalog(declaration), authorization.Options{})
	if calls != 0 {
		t.Fatal("factory ran in Build")
	}
	for _, roles := range [][]string{nil, {"Editor"}} {
		ctx := identity.WithPrincipal(t.Context(), identity.System(roles...))
		p, err := e.Prepare(ctx, create)
		if err != nil {
			t.Fatal(err)
		}
		scope, err := execution.OpenScope(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := p.EvaluateScoped(ctx, scope, "input")
		if err != nil || decision.IsAllowed() != (len(roles) > 0) {
			t.Fatalf("decision = %v, %v", decision, err)
		}
		if len(roles) == 0 && calls != 0 {
			t.Fatal("factory before role denial")
		}
		if len(roles) > 0 {
			if calls != 1 || !errors.Is(retained.CheckContext(ctx), execution.ErrScopeExpired) {
				t.Fatal("factory lifetime")
			}
			if decision, err := p.Evaluate(ctx, nil); decision.IsAllowed() || !errors.Is(err, authorization.ErrScopeRequired) {
				t.Fatal("direct scoped policy silently skipped", err)
			}
		}
		if err := scope.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScopedPolicyNilPanicFailureAndGuestOptIn(t *testing.T) {
	failure := errors.New("factory failed")
	for _, tc := range []struct {
		name    string
		factory authorization.Factory[authorization.Policy]
		want    error
	}{
		{"nil", func(context.Context, *execution.Scope) (authorization.Policy, error) {
			return authorization.PolicyFunc(nil), nil
		}, authorization.ErrInvalidConfiguration},
		{"error", func(context.Context, *execution.Scope) (authorization.Policy, error) { return nil, failure }, failure},
		{"panic", func(context.Context, *execution.Scope) (authorization.Policy, error) { panic("secret") }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var registry authorization.Registry
			if err := authorization.RegisterPolicy(&registry, "p", tc.factory, authorization.PolicyOptions{EvaluatesAnonymous: true}); err != nil {
				t.Fatal(err)
			}
			e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "p"}}}), authorization.Options{})
			ctx := t.Context()
			p, err := e.Prepare(ctx, create)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := execution.OpenScope(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			d, err := p.EvaluateScoped(ctx, scope, nil)
			if d.IsAllowed() || err == nil {
				t.Fatal("failure allowed")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if tc.want == nil {
				var panicError *execution.PanicError
				if !errors.As(err, &panicError) {
					t.Fatal(err)
				}
			}
			if err := scope.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEvaluatorCatalogCompatibilityChecksFrozenContent(t *testing.T) {
	var registry authorization.Registry
	declaration := &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Editor"}}}}
	c := catalog(declaration)
	e := build(t, &registry, c, authorization.Options{})
	if err := e.CheckCatalog(c); err != nil {
		t.Fatal(err)
	}
	if err := e.CheckCatalog(metadata.Catalog{Version: metadata.Version}); err != nil {
		t.Fatal("superset cannot serve empty subset", err)
	}
	declaration.Requirements[0].Roles[0] = "Reader"
	if !errors.Is(e.CheckCatalog(c), authorization.ErrCatalogMismatch) {
		t.Fatal("mutable declaration accepted")
	}
	c.Commands[0].Authorization = nil
	if !errors.Is(e.CheckCatalog(c), authorization.ErrCatalogMismatch) {
		t.Fatal("missing declaration accepted")
	}
	c.Commands[0].Type.Name = "Unknown"
	if !errors.Is(e.CheckCatalog(c), authorization.ErrCatalogMismatch) {
		t.Fatal("unknown target accepted")
	}
}

func TestScopedPoliciesRejectMismatchedScopeBeforeFactory(t *testing.T) {
	var registry authorization.Registry
	if err := authorization.RegisterPolicy(&registry, "p", func(context.Context, *execution.Scope) (authorization.Policy, error) {
		t.Fatal("factory invoked")
		return nil, nil
	}, authorization.PolicyOptions{EvaluatesAnonymous: true}); err != nil {
		t.Fatal(err)
	}
	e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "p"}}}), authorization.Options{})
	ctx := t.Context()
	p, err := e.Prepare(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := execution.OpenScope(identity.WithPrincipal(ctx, identity.Principal{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.EvaluateScoped(ctx, scope, nil); !errors.Is(err, execution.ErrIdentityChanged) {
		t.Fatal(err)
	}
	if err := scope.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
