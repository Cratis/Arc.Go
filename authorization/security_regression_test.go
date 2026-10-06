// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
)

func TestGuestPolicyReceivesEmptyPrincipalAndRetainsCallerContinuity(t *testing.T) {
	anonymous := identity.NewPrincipal(identity.PrincipalData{
		ID: "unverified", Name: "unverified name", Roles: []string{"Admin"},
		Claims: []identity.Claim{{Type: "sub", Value: "unverified"}},
	})
	ctx := identity.WithPrincipal(t.Context(), anonymous)
	var registry authorization.Registry
	calls := 0
	register(t, &registry, "guest", true, func(_ context.Context, value authorization.Context) (authorization.Decision, error) {
		calls++
		if !value.Principal.Equal(identity.Principal{}) {
			t.Errorf("guest policy received caller identity: %v", value.Principal)
		}
		return authorization.Allow(), nil
	})
	e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "guest"}}}), authorization.Options{})
	prepared, err := e.Prepare(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Check(ctx); err != nil {
		t.Fatalf("original anonymous caller failed continuity: %v", err)
	}
	if decision, err := prepared.Evaluate(ctx, nil); err != nil || !decision.IsAllowed() || calls != 1 {
		t.Fatalf("guest decision = %v, error = %v, calls = %d", decision, err, calls)
	}
	for _, changed := range []context.Context{
		identity.WithPrincipal(ctx, identity.Principal{}),
		identity.WithPrincipal(ctx, identity.NewPrincipal(identity.PrincipalData{ID: "other"})),
		identity.WithPrincipal(ctx, identity.System()),
		t.Context(),
	} {
		if err := prepared.Check(changed); !errors.Is(err, authorization.ErrIdentityChanged) {
			t.Errorf("changed caller passed continuity: %v", err)
		}
		if decision, err := prepared.Evaluate(changed, nil); decision.IsAllowed() || !errors.Is(err, authorization.ErrIdentityChanged) {
			t.Errorf("changed caller evaluated: %v, %v", decision, err)
		}
	}
	if calls != 1 {
		t.Fatal("policy ran after anonymous identity changed")
	}
}

func TestGuestIdentityChangeDuringPolicyInvalidatesVerdict(t *testing.T) {
	for _, replacement := range []identity.Principal{
		identity.NewPrincipal(identity.PrincipalData{ID: "changed anonymous"}),
		identity.System(),
	} {
		ctx := &switchingContext{Context: t.Context(), values: identity.WithPrincipal(t.Context(), identity.NewPrincipal(identity.PrincipalData{ID: "original"}))}
		var registry authorization.Registry
		register(t, &registry, "guest", true, func(context.Context, authorization.Context) (authorization.Decision, error) {
			ctx.values = identity.WithPrincipal(t.Context(), replacement)
			return authorization.Allow(), nil
		})
		e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "guest"}}}), authorization.Options{})
		prepared, err := e.Prepare(ctx, create)
		if err != nil {
			t.Fatal(err)
		}
		if decision, err := prepared.Evaluate(ctx, nil); decision.IsAllowed() || !errors.Is(err, authorization.ErrIdentityChanged) {
			t.Fatalf("identity changed in policy: %v, %v", decision, err)
		}
	}
}

func TestReadModelDeclarationsMustAgree(t *testing.T) {
	protected := &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Reader"}}}}
	for _, tc := range []struct {
		name      string
		a, b      *metadata.Authorization
		wantError bool
	}{
		{"both undeclared", nil, nil, false},
		{"equal independent declarations", protected, &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Reader"}}}}, false},
		{"empty lists", &metadata.Authorization{}, &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{}}, false},
		{"missing second declaration", protected, nil, true},
		{"missing first declaration", nil, protected, true},
		{"empty vs undeclared", nil, &metadata.Authorization{}, true},
		{"different roles", protected, &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Admin"}}}}, true},
		{"different anonymous access", &metadata.Authorization{}, &metadata.Authorization{AllowAnonymous: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := metadata.Catalog{Version: metadata.Version, Queries: []metadata.Query{
				{ReadModel: metadata.TypeName{Namespace: "App", Name: "Item"}, Name: "All", ReadModelAuthorization: tc.a, Authorization: &metadata.Authorization{AllowAnonymous: true}},
				{ReadModel: metadata.TypeName{Namespace: "App", Name: "Item"}, Name: "ByID", ReadModelAuthorization: tc.b, Authorization: &metadata.Authorization{AllowAnonymous: true}},
			}}
			var registry authorization.Registry
			_, err := registry.Build(c, authorization.Options{})
			if tc.wantError {
				if !errors.Is(err, authorization.ErrInvalidConfiguration) {
					t.Fatalf("mismatched declarations: %v", err)
				}
				// Different namespace-qualified read models need not agree, and
				// a failed build must leave the registry editable.
				c.Queries[1].ReadModel.Namespace = "Other"
				build(t, &registry, c, authorization.Options{})
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnknownRuntimeTargetIsRedactedAndNotConfiguration(t *testing.T) {
	var registry authorization.Registry
	e := build(t, &registry, catalog(nil), authorization.Options{})
	identity := "untrusted-request-identity"
	for _, evaluator := range []*authorization.Evaluator{e, nil} {
		_, err := evaluator.Prepare(t.Context(), authorization.Target{Kind: authorization.Command, Identity: identity})
		if !errors.Is(err, authorization.ErrUnknownTarget) || errors.Is(err, authorization.ErrInvalidConfiguration) {
			t.Fatalf("unknown target category: %v", err)
		}
		if strings.Contains(err.Error(), identity) || err.Error() != authorization.ErrUnknownTarget.Error() {
			t.Fatalf("unknown target text: %q", err)
		}
		var configuration *authorization.ConfigurationError
		if errors.As(err, &configuration) {
			t.Fatal("runtime lookup returned a configuration error")
		}
	}
}
