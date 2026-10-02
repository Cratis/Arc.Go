// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/services"
	"github.com/cratis/arc.go/tenancy"
)

type item struct{ number int64 }
type scopedItem item
type transientItem item

func register[T any](t *testing.T, r *services.Registry, lifetime services.Lifetime, factory func(context.Context, *services.Scope) (T, error), deps ...services.Key) {
	t.Helper()
	if err := services.Bind(r, lifetime, factory, deps...); err != nil {
		t.Fatal(err)
	}
}
func provider(t *testing.T, r *services.Registry) *services.Provider {
	t.Helper()
	p, err := r.Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return p
}
func scope(t *testing.T, p *services.Provider, ctx context.Context) *services.Scope {
	t.Helper()
	s, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}
func resolved[T any](t *testing.T, ctx context.Context, s *services.Scope) T {
	t.Helper()
	v, err := services.Resolve[T](ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLifetimeCachingAndOwnership(t *testing.T) {
	r := &services.Registry{}
	var count atomic.Int64
	register(t, r, services.Singleton, func(context.Context, *services.Scope) (*item, error) { return &item{number: count.Add(1)}, nil })
	register(t, r, services.Scoped, func(context.Context, *services.Scope) (*scopedItem, error) {
		return &scopedItem{number: count.Add(1)}, nil
	})
	register(t, r, services.Transient, func(context.Context, *services.Scope) (*transientItem, error) {
		return &transientItem{number: count.Add(1)}, nil
	})
	p := provider(t, r)
	ctx := context.Background()
	s1 := scope(t, p, ctx)
	s2 := scope(t, p, ctx)
	if !p.Contains(services.KeyFor[*item]()) || p.Contains(services.KeyFor[item]()) || !p.Owns(s1) || p.Owns(&services.Scope{}) {
		t.Fatal("provider introspection")
	}
	if resolved[*item](t, ctx, s1) != resolved[*item](t, ctx, s2) {
		t.Fatal("singleton")
	}
	scopedFirst := resolved[*scopedItem](t, ctx, s1)
	if scopedFirst != resolved[*scopedItem](t, ctx, s1) || scopedFirst == resolved[*scopedItem](t, ctx, s2) {
		t.Fatal("scoped")
	}
	transientFirst := resolved[*transientItem](t, ctx, s1)
	if transientFirst == resolved[*transientItem](t, ctx, s1) {
		t.Fatal("transient")
	}
	if _, err := services.Resolve[item](ctx, s1); !errors.Is(err, services.ErrMissing) {
		t.Fatal(err)
	}
	if _, err := services.Resolve[item](ctx, &services.Scope{}); !errors.Is(err, services.ErrInvalidScope) {
		t.Fatal(err)
	}
	if err := (&services.Provider{}).Close(ctx); !errors.Is(err, services.ErrInvalidScope) {
		t.Fatal(err)
	}
}
func TestScopeMetadataIsolation(t *testing.T) {
	r := &services.Registry{}
	if err := services.BindValue(r, 7); err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.System("jobs"))
	ctx = tenancy.WithTenant(ctx, tenancy.Default())
	p := provider(t, r)
	s := scope(t, p, ctx)
	for _, other := range []context.Context{context.Background(), identity.WithPrincipal(ctx, identity.System("other")), tenancy.WithTenant(ctx, tenancy.ID{}), identity.WithPrincipal(ctx, identity.Principal{})} {
		if err := s.CheckContext(other); !errors.Is(err, services.ErrContextMismatch) {
			t.Fatal(err)
		}
		if _, err := services.Resolve[int](other, s); !errors.Is(err, services.ErrContextMismatch) {
			t.Fatal(err)
		}
	}
	changed := execution.WithReceivedAt(correlation.WithID(ctx, correlation.ID{}), execution.Capture(ctx).ReceivedAt)
	if got := resolved[int](t, changed, s); got != 7 {
		t.Fatal(got)
	}
	absent := scope(t, p, context.Background())
	if err := absent.CheckContext(identity.WithPrincipal(context.Background(), identity.Principal{})); !errors.Is(err, services.ErrContextMismatch) {
		t.Fatal("presence", err)
	}
	if err := absent.CheckContext(tenancy.WithTenant(context.Background(), tenancy.ID{})); !errors.Is(err, services.ErrContextMismatch) {
		t.Fatal("presence", err)
	}
}
func TestDeclaredDependenciesAndExpiredViews(t *testing.T) {
	r := &services.Registry{}
	var saved *services.Scope
	calls := 0
	register(t, r, services.Transient, func(context.Context, *services.Scope) (*beta, error) { calls++; return &beta{}, nil })
	register(t, r, services.Transient, func(ctx context.Context, s *services.Scope) (*alpha, error) {
		saved = s
		_, err := services.Resolve[*beta](ctx, s)
		return &alpha{}, err
	})
	p := provider(t, r)
	ctx := context.Background()
	s := scope(t, p, ctx)
	if _, err := services.Resolve[*alpha](ctx, s); !errors.Is(err, services.ErrUndeclaredDependency) || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := services.Resolve[*beta](ctx, saved); !errors.Is(err, services.ErrFactoryScopeExpired) {
		t.Fatal(err)
	}
	if err := saved.Close(ctx); !errors.Is(err, services.ErrInvalidScope) {
		t.Fatal(err)
	}
	if err := saved.CheckContext(ctx); !errors.Is(err, services.ErrFactoryScopeExpired) {
		t.Fatal(err)
	}
}
func TestSingletonHidesAllRequestValuesAndPreservesDeadline(t *testing.T) {
	type privateKey struct{}
	r := &services.Registry{}
	var rootScope *services.Scope
	register(t, r, services.Transient, func(ctx context.Context, _ *services.Scope) (*beta, error) {
		if _, ok := identity.PrincipalFrom(ctx); ok {
			t.Error("principal leaked")
		}
		if _, ok := tenancy.TenantFrom(ctx); ok {
			t.Error("tenant leaked")
		}
		if _, ok := execution.ReceivedAt(ctx); ok {
			t.Error("receipt leaked")
		}
		if ctx.Value(privateKey{}) != nil {
			t.Error("value leaked")
		}
		return &beta{}, nil
	})
	register(t, r, services.Singleton, func(ctx context.Context, s *services.Scope) (*alpha, error) {
		rootScope = s
		if _, ok := ctx.Deadline(); !ok {
			t.Error("deadline lost")
		}
		_, err := services.Resolve[*beta](ctx, s)
		return &alpha{}, err
	}, services.KeyFor[*beta]())
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), privateKey{}, "secret"))
	defer cancel()
	ctx, cancelDeadline := context.WithTimeout(ctx, 10000000000)
	defer cancelDeadline()
	ctx, err := execution.NewContext(ctx, execution.Metadata{Principal: identity.System("jobs"), Tenant: tenancy.Default()})
	if err != nil {
		t.Fatal(err)
	}
	p := provider(t, r)
	s := scope(t, p, ctx)
	resolved[*alpha](t, ctx, s)
	if p.Owns(rootScope) {
		t.Fatal("factory view mistaken for ordinary scope")
	}
}
func ExampleResolve() {
	var registry services.Registry
	if err := services.BindValue(&registry, "configured value"); err != nil {
		panic(err)
	}
	p, err := registry.Build()
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	s, err := p.NewScope(ctx)
	if err != nil {
		panic(err)
	}
	value, err := services.Resolve[string](ctx, s)
	if err != nil {
		panic(err)
	}
	fmt.Println(value)
	if err := s.Close(ctx); err != nil {
		panic(err)
	}
	if err := p.Close(ctx); err != nil {
		panic(err)
	}
	// Output: configured value
}
