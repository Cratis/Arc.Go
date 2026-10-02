package authorization_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/tenancy"
)

func evaluate(t *testing.T, e *authorization.Evaluator, ctx context.Context, target authorization.Target) authorization.Decision {
	t.Helper()
	prepared, err := e.Prepare(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := prepared.Evaluate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}
func TestRolesAndPolicyOrdering(t *testing.T) {
	var registry authorization.Registry
	var calls []string
	for _, name := range []string{"first", "second"} {
		register(t, &registry, name, false, func(_ context.Context, value authorization.Context) (authorization.Decision, error) {
			calls = append(calls, name)
			if value.Target != create || value.Resource != "resource" || !value.ReceivedAt.Equal(time.Unix(1, 0)) {
				t.Error("policy input")
			}
			return authorization.Allow(), nil
		})
	}
	declaration := &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Writer", "Owner"}, Policy: "first"}, {Roles: []string{"Approved"}, Policy: "second"}}}
	evaluator := build(t, &registry, catalog(declaration), authorization.Options{})
	declaration.Requirements[0].Roles[0] = "mutated"
	declaration.Requirements[0].Policy = "missing"
	for _, roles := range [][]string{nil, {"Writer"}, {"Owner", "Approved"}, {"Writer", "Approved"}, {"writer", "Approved"}} {
		calls = nil
		ctx := execution.WithReceivedAt(identity.WithPrincipal(t.Context(), identity.System(roles...)), time.Unix(1, 0))
		prepared, err := evaluator.Prepare(ctx, create)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := prepared.Evaluate(ctx, "resource")
		if err != nil {
			t.Fatal(err)
		}
		want := len(roles) == 2 && roles[0] != "writer"
		if decision.IsAllowed() != want {
			t.Fatalf("roles %v allowed=%v", roles, decision.IsAllowed())
		}
		if want && !reflect.DeepEqual(calls, []string{"first", "second"}) {
			t.Fatal(calls)
		}
		if !want && len(calls) != 0 {
			t.Fatal("policy executed before role denial")
		}
	}
}

func TestPrecedenceAndFallback(t *testing.T) {
	fallback := &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Admin"}}}}
	c := catalog(nil)
	c.Queries = []metadata.Query{
		{ReadModel: metadata.TypeName{Name: "Item"}, Name: "Public", Authorization: &metadata.Authorization{AllowAnonymous: true}, ReadModelAuthorization: fallback},
		{ReadModel: metadata.TypeName{Name: "Item"}, Name: "Restricted", Authorization: &metadata.Authorization{}, ReadModelAuthorization: fallback},
		{ReadModel: metadata.TypeName{Name: "Item"}, Name: "Inherited", ReadModelAuthorization: fallback},
	}
	var registry authorization.Registry
	e := build(t, &registry, c, authorization.Options{Fallback: fallback})
	fallback.Requirements[0].Roles[0] = "mutated"
	guest := t.Context()
	admin := identity.WithPrincipal(guest, identity.System("Admin"))
	for _, tc := range []struct {
		target authorization.Target
		ctx    context.Context
		want   bool
	}{
		{create, guest, false}, {create, admin, true},
		{authorization.Target{Kind: authorization.Query, Identity: "Item.Public"}, guest, true},
		{authorization.Target{Kind: authorization.Query, Identity: "Item.Restricted"}, guest, false},
		{authorization.Target{Kind: authorization.Query, Identity: "Item.Restricted"}, identity.WithPrincipal(guest, identity.System()), true},
		{authorization.Target{Kind: authorization.Query, Identity: "Item.Inherited"}, guest, false},
	} {
		if got := evaluate(t, e, tc.ctx, tc.target); got.IsAllowed() != tc.want {
			t.Fatalf("%v = %v", tc.target, got.IsAllowed())
		}
	}
	var public authorization.Registry
	if !evaluate(t, build(t, &public, catalog(nil), authorization.Options{}), guest, create).IsAllowed() {
		t.Fatal("undeclared not public")
	}
}

func TestGuestConjunction(t *testing.T) {
	for _, requirements := range [][]metadata.AuthorizationRequirement{
		{{Policy: "guest"}}, {{Policy: "guest"}, {Policy: "otherGuest"}}, {{Policy: "guest"}, {Policy: "authenticated"}}, {{Policy: "guest"}, {Roles: []string{"Reader"}}}, {{Policy: "guest"}, {}}, nil,
	} {
		var registry authorization.Registry
		var calls int
		for _, name := range []string{"guest", "otherGuest", "authenticated"} {
			register(t, &registry, name, name != "authenticated", func(context.Context, authorization.Context) (authorization.Decision, error) {
				calls++
				return authorization.Allow(), nil
			})
		}
		e := build(t, &registry, catalog(&metadata.Authorization{Requirements: requirements}), authorization.Options{})
		decision := evaluate(t, e, t.Context(), create)
		want := len(requirements) > 0
		for _, requirement := range requirements {
			if requirement.Policy == "" || requirement.Policy == "authenticated" {
				want = false
			}
		}
		if decision.IsAllowed() != want || (!want && calls != 0) {
			t.Fatalf("guest requirements=%v allowed=%v calls=%d", requirements, decision.IsAllowed(), calls)
		}
	}
}

func TestPreparedSecurityAndZero(t *testing.T) {
	var registry authorization.Registry
	calls := 0
	register(t, &registry, "policy", false, func(context.Context, authorization.Context) (authorization.Decision, error) {
		calls++
		return authorization.Allow(), nil
	})
	e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Admin"}, Policy: "policy"}}}), authorization.Options{})
	ctx := identity.WithPrincipal(t.Context(), identity.System())
	prepared, err := e.Prepare(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("Prepare invoked policy")
	}
	denied, err := prepared.Evaluate(ctx, nil)
	if err != nil || denied.IsAllowed() || calls != 0 {
		t.Fatalf("role gate %v %v", denied, err)
	}
	changed := identity.WithPrincipal(ctx, identity.System("Admin"))
	if err := prepared.Check(changed); !errors.Is(err, authorization.ErrIdentityChanged) {
		t.Fatal(err)
	}
	tenant, _ := tenancy.ParseID("other")
	for _, other := range []context.Context{tenancy.WithTenant(ctx, tenant), tenancy.WithTenant(ctx, tenancy.ID{})} {
		if d, err := prepared.Evaluate(other, nil); d.IsAllowed() || !errors.Is(err, authorization.ErrIdentityChanged) {
			t.Fatal("tenant/presence", err)
		}
	}
	if _, err := e.Prepare(ctx, authorization.Target{}); !errors.Is(err, authorization.ErrUnknownTarget) {
		t.Fatal(err)
	}
	var zero authorization.Prepared
	if err := zero.Check(ctx); !errors.Is(err, authorization.ErrNotPrepared) {
		t.Fatal(err)
	}
	if d, err := zero.Evaluate(ctx, nil); d.IsAllowed() || !errors.Is(err, authorization.ErrNotPrepared) {
		t.Fatal(err)
	}
}

func TestPolicyFailureCancellationAndConcurrency(t *testing.T) {
	sentinel := errors.New("secret")
	for _, callback := range []authorization.PolicyFunc{
		func(context.Context, authorization.Context) (authorization.Decision, error) {
			return authorization.Allow(), sentinel
		},
		func(context.Context, authorization.Context) (authorization.Decision, error) {
			return authorization.Deny("reason"), nil
		},
	} {
		var registry authorization.Registry
		register(t, &registry, "policy", true, callback)
		e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "policy"}}}), authorization.Options{})
		p, err := e.Prepare(t.Context(), create)
		if err != nil {
			t.Fatal(err)
		}
		d, err := p.Evaluate(t.Context(), nil)
		if d.IsAllowed() || (err != nil && err != sentinel) || !errors.Is(d.Err(), authorization.ErrDenied) {
			t.Fatalf("fail closed=%v %v", d, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	var registry authorization.Registry
	register(t, &registry, "policy", true, func(context.Context, authorization.Context) (authorization.Decision, error) {
		cancel()
		return authorization.Allow(), nil
	})
	e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "policy"}}}), authorization.Options{})
	p, err := e.Prepare(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := p.Evaluate(ctx, nil); d.IsAllowed() || err != context.Canceled {
		t.Fatalf("cancellation=%v %v", d, err)
	}
	var concurrent authorization.Registry
	register(t, &concurrent, "tenant", true, func(ctx context.Context, value authorization.Context) (authorization.Decision, error) {
		tenant, _ := tenancy.TenantFrom(ctx)
		if tenant != value.Tenant || value.Resource != tenant.String() {
			t.Error("tenant contamination")
		}
		return authorization.Allow(), nil
	})
	e = build(t, &concurrent, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "tenant"}}}), authorization.Options{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			tenant, err := tenancy.ParseID(fmt.Sprint(i))
			if err != nil {
				t.Error(err)
				return
			}
			ctx := tenancy.WithTenant(t.Context(), tenant)
			p, err := e.Prepare(ctx, create)
			if err != nil {
				t.Error(err)
				return
			}
			d, err := p.Evaluate(ctx, tenant.String())
			if err != nil || !d.IsAllowed() {
				t.Errorf("%v %v", d, err)
			}
		})
	}
	wg.Wait()
}

func ExampleRegistry() {
	var registry authorization.Registry
	err := registry.Register("owner", authorization.PolicyFunc(func(_ context.Context, value authorization.Context) (authorization.Decision, error) {
		if value.Principal.ID() == value.Resource {
			return authorization.Allow(), nil
		}
		return authorization.Deny("not owner"), nil
	}), authorization.PolicyOptions{})
	if err != nil {
		panic(err)
	}
	e, err := registry.Build(catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "owner"}}}), authorization.Options{})
	if err != nil {
		panic(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.NewPrincipal(identity.PrincipalData{ID: "alice", AuthenticationType: "verified"}))
	prepared, err := e.Prepare(ctx, create)
	if err != nil {
		panic(err)
	}
	decision, err := prepared.Evaluate(ctx, "alice")
	if err != nil {
		panic(err)
	}
	fmt.Println(decision.IsAllowed())
	// Output: true
}
