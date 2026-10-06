// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package identity_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/cratis/arc.go/identity"
)

func TestPrincipalSnapshot(t *testing.T) {
	data := identity.PrincipalData{ID: "subject", Name: "Name", AuthenticationType: "verified", Roles: []string{"Admin", "Admin"}, Claims: []identity.Claim{{Type: "sub", Value: "first"}, {Type: "sub", Value: "second"}}}
	p := identity.NewPrincipal(data)
	copy := identity.NewPrincipal(data)
	data.Roles[0] = "mutated"
	data.Claims[0].Value = "mutated"
	p.Roles()[0] = "mutated"
	p.Claims()[0].Value = "mutated"
	if !p.Equal(copy) || !p.HasRole("Admin") || p.HasRole("admin") {
		t.Fatal("snapshot or role semantics")
	}
	if value, ok := p.Claim("sub"); !ok || value != "first" {
		t.Fatal(value, ok)
	}
	if _, ok := p.Claim("SUB"); ok {
		t.Fatal("case insensitive claim")
	}
	if identity.NewPrincipal(identity.PrincipalData{ID: "subject", Roles: []string{"Admin"}, Claims: []identity.Claim{{Type: "role", Value: "Admin"}}}).HasRole("Admin") {
		t.Fatal("anonymous roles granted")
	}
	for _, changed := range []identity.PrincipalData{{ID: "different"}, {Name: "different"}, {AuthenticationType: "different"}, {Roles: []string{"Admin"}}, {Claims: []identity.Claim{{Type: "sub", Value: "second"}, {Type: "sub", Value: "first"}}}} {
		if p.Equal(identity.NewPrincipal(changed)) {
			t.Fatal("equality omitted fields")
		}
	}
}

func TestSystemRoles(t *testing.T) {
	p := identity.System("jobs")
	if p.ID() != "[System]" || p.Name() != "[System]" || p.AuthenticationType() != "System" || !p.HasRole("jobs") || p.HasRole("admin") {
		t.Fatal("system bypass or invalid actor")
	}
	if got := p.Claims(); len(got) != 4 || got[1] != (identity.Claim{Type: "sub", Value: "[System]"}) || got[3].Type != "http://schemas.microsoft.com/ws/2008/06/identity/claims/role" {
		t.Fatal(got)
	}
}

func TestPrincipalContexts(t *testing.T) {
	root := context.Background()
	if _, ok := identity.PrincipalFrom(root); ok {
		t.Fatal("absent")
	}
	parent := identity.WithPrincipal(root, identity.System("parent"))
	child := identity.WithPrincipal(parent, identity.Principal{})
	if p, ok := identity.PrincipalFrom(child); !ok || p.IsAuthenticated() {
		t.Fatal("shadow")
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			p := identity.System(fmt.Sprint(i))
			ctx := identity.WithPrincipal(parent, p)
			got, ok := identity.PrincipalFrom(ctx)
			if !ok || !got.Equal(p) {
				t.Error("isolation")
			}
		})
	}
	wg.Wait()
	if p, _ := identity.PrincipalFrom(parent); !p.HasRole("parent") {
		t.Fatal("parent changed")
	}
}

func ExampleSystem() {
	ctx := identity.WithPrincipal(context.Background(), identity.System("jobs"))
	p, _ := identity.PrincipalFrom(ctx)
	fmt.Println(p.ID(), p.HasRole("jobs"), p.HasRole("admin"))
	// Output: [System] true false
}
