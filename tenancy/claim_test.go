// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy_test

import (
	"testing"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

func TestClaimSelectorIgnoresUnauthenticatedIdentity(t *testing.T) {
	r := resolver(t, tenancy.Options{Strategy: tenancy.Claim})
	for _, value := range []string{"tenant", " invalid tenant "} {
		principal := identity.NewPrincipal(identity.PrincipalData{ID: "unverified", Claims: []identity.Claim{{Type: "tenant_id", Value: value}}})
		ctx := identity.WithPrincipal(t.Context(), principal)
		if got, err := r.Resolve(ctx, nil); err != nil || got.IsSet() || got.String() != "[NotSet]" {
			t.Errorf("anonymous claim %q selected %v: %v", value, got, err)
		}
	}
	if got, err := r.Resolve(t.Context(), nil); err != nil || got.IsSet() {
		t.Fatalf("absent principal selected %v: %v", got, err)
	}
	principal := identity.NewPrincipal(identity.PrincipalData{AuthenticationType: "verified", Claims: []identity.Claim{{Type: "tenant_id", Value: "tenant"}}})
	if got, err := r.Resolve(identity.WithPrincipal(t.Context(), principal), nil); err != nil || got.String() != "tenant" {
		t.Fatalf("authenticated claim = %v: %v", got, err)
	}
}
