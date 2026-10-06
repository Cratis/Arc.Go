// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
)

// These explicit foundation calls specify seams, not an implemented pipeline or
// HTTP status contract. Admission sequencing remains the host/pipeline's job.
func TestSecuritySeamsRemainIndependent(t *testing.T) {
	principal := identity.NewPrincipal(identity.PrincipalData{ID: "reader", AuthenticationType: "verified", Roles: []string{"Reader"}})
	ctx := identity.WithPrincipal(t.Context(), principal)
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("x-cratis-tenant-id", "acme")
	chain, err := authentication.New(authentication.HostPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := chain.Authenticate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	actor, ok := authenticated.Principal()
	if !ok || !actor.Equal(principal) {
		t.Fatal("trusted actor lost")
	}
	resolver, err := tenancy.NewResolver(tenancy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := resolver.Resolve(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	ctx = tenancy.WithTenant(ctx, tenant)
	catalog := metadata.Catalog{Version: 1, Commands: []metadata.Command{
		{Type: metadata.TypeName{Name: "Public"}, Authorization: &metadata.Authorization{AllowAnonymous: true}},
		{Type: metadata.TypeName{Name: "Restricted"}, Authorization: &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Admin"}}}}},
	}}
	var registry authorization.Registry
	evaluator, err := registry.Build(catalog, authorization.Options{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := evaluator.Prepare(ctx, authorization.Target{Kind: authorization.Command, Identity: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	public, err := prepared.Evaluate(ctx, nil)
	if err != nil || !public.IsAllowed() {
		t.Fatal("public declaration", err)
	}
	membership := tenancy.MembershipFunc(func(_ context.Context, p identity.Principal, id tenancy.ID) (bool, error) {
		return p.HasRole("Member") && id == tenant, nil
	})
	member, err := membership.Authorize(ctx, actor, tenant)
	if err != nil || member {
		t.Fatal("public bypassed membership", err)
	}
	findings, err := validation.Invoke(ctx, validation.ValidatorFunc[int](func(context.Context, int) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Warning, Message: "Check input."}}, nil
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	var policy validation.Policy
	if len(policy.Filter(findings)) != 0 {
		t.Fatal("warning blocks default policy")
	}
	restricted, err := evaluator.Prepare(ctx, authorization.Target{Kind: authorization.Command, Identity: "Restricted"})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := restricted.Evaluate(ctx, nil)
	if err != nil || verdict.IsAllowed() || !errors.Is(verdict.Err(), authorization.ErrDenied) {
		t.Fatal("nonblocking warning became permission", err)
	}
	details, err := identity.ProvideDetails(ctx, identity.DetailsProviderFunc[map[string]any](func(context.Context, identity.Context) (identity.Details[map[string]any], error) {
		return identity.Details[map[string]any]{IsUserAuthorized: true, Value: map[string]any{"roles": []string{"Admin"}, "isAuthenticated": true}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	details.Roles[0] = "Admin"
	if err := restricted.Check(ctx); err != nil {
		t.Fatal(err)
	}
	verdict, err = restricted.Evaluate(ctx, nil)
	if err != nil || verdict.IsAllowed() {
		t.Fatal("display details elevated authority", err)
	}
	failedChain, err := authentication.New(authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
		return authentication.Failed("invalid credential"), nil
	}), authentication.HostPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	failure, err := failedChain.Authenticate(ctx, request)
	if err != nil || failure.Failure() == nil {
		t.Fatal("failed credentials rescued", err)
	}
	if _, ok := failure.Principal(); ok {
		t.Fatal("failed chain retained authority")
	}
}
