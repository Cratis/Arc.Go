// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
	"github.com/cratis/fundamentals.go/dependencyinjection/ditest"
)

type testDIScope struct {
	resolve func(context.Context, di.Key) (any, error)
	closes  atomic.Int64
	failure error
}

func (s *testDIScope) Resolve(ctx context.Context, key di.Key) (any, error) {
	return s.resolve(ctx, key)
}
func (s *testDIScope) Close(context.Context) error { s.closes.Add(1); return s.failure }

type testScopeFactory struct {
	open func(context.Context) (di.Scope, error)
}

func (f *testScopeFactory) NewScope(ctx context.Context) (di.Scope, error) { return f.open(ctx) }

type testScopeOwner struct {
	*testScopeFactory
	scope di.Scope
}

func (f *testScopeOwner) Owns(s di.Scope) bool { return s == f.scope }

func borrowDIScope(t *testing.T, ctx context.Context, resources execution.Resources) *execution.Scope {
	t.Helper()
	scope, err := execution.BorrowScope(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return scope
}

func TestGuardedResolverConformsToFundamentals(t *testing.T) {
	ditest.RunResolver(t, func(t *testing.T, values map[di.Key]any) di.Resolver {
		resources := &testDIScope{resolve: func(ctx context.Context, key di.Key) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			value, ok := values[key]
			if !ok {
				return nil, di.ErrMissing
			}
			return value, nil
		}}
		scope, err := execution.BorrowScope(context.Background(), resources)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := scope.Close(context.Background()); err != nil {
				t.Error(err)
			}
			if resources.closes.Load() != 0 {
				t.Error("disposed borrowed resources")
			}
		})
		return scope.Resolver()
	})
}

func TestResolverRejectsInvalidEmptyClosedAndExpiredScopes(t *testing.T) {
	ctx := context.Background()
	for _, scope := range []*execution.Scope{nil, {}} {
		if _, err := scope.Resolver().Resolve(ctx, di.KeyFor[int]()); !errors.Is(err, execution.ErrInvalidScope) {
			t.Fatal(err)
		}
		if _, err := execution.Resolve[int](ctx, scope); !errors.Is(err, execution.ErrInvalidScope) {
			t.Fatal(err)
		}
	}
	for _, resources := range []execution.Resources{nil, &holder{}} {
		scope := borrowDIScope(t, ctx, resources)
		if _, err := execution.Resolve[int](ctx, scope); !errors.Is(err, execution.ErrNoResolver) {
			t.Fatal(err)
		}
	}
	resources := &testDIScope{resolve: func(context.Context, di.Key) (any, error) { return 42, nil }}
	scope := borrowDIScope(t, ctx, resources)
	var retained di.Resolver
	if err := scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
		retained = view.Resolver()
		if _, ok := retained.(di.Scope); ok {
			t.Error("resolver exposes closing scope")
		}
		value, err := execution.Resolve[int](ctx, view)
		if value != 42 {
			t.Error(value)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := retained.Resolve(ctx, di.KeyFor[int]()); !errors.Is(err, execution.ErrScopeExpired) {
		t.Fatal(err)
	}
	if err := scope.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.Resolve[int](ctx, scope); !errors.Is(err, execution.ErrScopeClosed) {
		t.Fatal(err)
	}
}

func TestResolutionChecksSecurityBeforeAndAfterAndDropsFailedValues(t *testing.T) {
	base := context.Background()
	ctx := &switchContext{Context: base, current: base}
	calls := 0
	failure := errors.New("resolve failed")
	resources := &testDIScope{resolve: func(context.Context, di.Key) (any, error) {
		calls++
		ctx.current = identity.WithPrincipal(base, identity.Principal{})
		return 42, failure
	}}
	scope := borrowDIScope(t, ctx, resources)
	changed := tenancy.WithTenant(base, tenancy.Default())
	if _, err := execution.Resolve[int](changed, scope); !errors.Is(err, execution.ErrIdentityChanged) || calls != 0 {
		t.Fatal(err, calls)
	}
	value, err := scope.Resolver().Resolve(ctx, di.KeyFor[int]())
	if value != nil || !errors.Is(err, failure) || !errors.Is(err, execution.ErrIdentityChanged) || calls != 1 {
		t.Fatal(value, err, calls)
	}
}

func TestResolutionAdmissionJoinsClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		entered, release := make(chan struct{}), make(chan struct{})
		resources := &testDIScope{resolve: func(context.Context, di.Key) (any, error) {
			close(entered)
			<-release
			return 42, nil
		}}
		scope, err := execution.OpenScope(ctx, func(context.Context) (execution.Resources, error) { return resources, nil })
		if err != nil {
			t.Fatal(err)
		}
		resolved, closed := make(chan error, 1), make(chan error, 1)
		go func() {
			value, err := execution.Resolve[int](ctx, scope)
			if value != 42 {
				t.Error(value)
			}
			resolved <- err
		}()
		<-entered
		go func() { closed <- scope.Close(ctx) }()
		synctest.Wait()
		if resources.closes.Load() != 0 {
			t.Error("closed during admitted resolution")
		}
		if _, err := execution.Resolve[int](ctx, scope); !errors.Is(err, execution.ErrScopeClosed) {
			t.Fatal(err)
		}
		close(release)
		if err := <-resolved; err != nil {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if resources.closes.Load() != 1 {
			t.Fatal("cleanup count", resources.closes.Load())
		}
	})
}

func TestResourcesFromInvalidFactoriesAndFailedOpening(t *testing.T) {
	ctx := context.Background()
	failure, cleanup := errors.New("open"), errors.New("cleanup")
	for _, factory := range []di.ScopeFactory{nil, (*testScopeFactory)(nil)} {
		if _, err := execution.OpenScope(ctx, execution.ResourcesFrom(factory)); !errors.Is(err, execution.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	for _, resources := range []di.Scope{nil, (*testDIScope)(nil)} {
		factory := &testScopeFactory{open: func(context.Context) (di.Scope, error) { return resources, failure }}
		if _, err := execution.OpenScope(ctx, execution.ResourcesFrom(factory)); !errors.Is(err, execution.ErrResourceType) || !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	resources := &testDIScope{failure: cleanup}
	factory := &testScopeFactory{open: func(context.Context) (di.Scope, error) { return resources, failure }}
	if scope, err := execution.OpenScope(ctx, execution.ResourcesFrom(factory)); scope != nil || !errors.Is(err, failure) || !errors.Is(err, cleanup) || resources.closes.Load() != 1 {
		t.Fatal(scope, err, resources.closes.Load())
	}
	calls := 0
	factory.open = func(context.Context) (di.Scope, error) { calls++; return resources, nil }
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := execution.ResourcesFrom(factory)(canceled); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := execution.ResourcesFrom(factory)(nil); !errors.Is(err, execution.ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestBorrowedScopeOwnershipAndCloseOnceForwarding(t *testing.T) {
	ctx := context.Background()
	p := jobProvider(t, func(context.Context, di.Resolver) (*jobResource, error) {
		return &jobResource{close: func(context.Context) error { return nil }}, nil
	})
	other := jobProvider(t, func(context.Context, di.Resolver) (*jobResource, error) {
		return &jobResource{close: func(context.Context) error { return nil }}, nil
	})
	t.Cleanup(func() {
		if err := p.Close(ctx); err != nil {
			t.Error(err)
		}
		if err := other.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	raw, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	borrowed := borrowDIScope(t, ctx, raw)
	if err := borrowed.CheckOwner(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := borrowed.CheckOwner(ctx, other); !errors.Is(err, execution.ErrScopeOwner) {
		t.Fatal(err)
	}
	if err := borrowed.CheckOwner(ctx, nil); !errors.Is(err, execution.ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := borrowDIScope(t, ctx, &holder{}).CheckOwner(ctx, p); !errors.Is(err, execution.ErrScopeOwner) {
		t.Fatal(err)
	}
	if err := borrowed.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := di.Resolve[*jobResource](ctx, raw); err != nil {
		t.Fatal("borrow closed underlying scope", err)
	}
	// Arc forwards Close once, even if an adapter does not make Close idempotent.
	cleanup := errors.New("cleanup")
	resources := &testDIScope{failure: cleanup}
	factory := &testScopeFactory{open: func(context.Context) (di.Scope, error) { return resources, nil }}
	owned, err := execution.OpenScope(ctx, execution.ResourcesFrom(factory))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := owned.Close(ctx); !errors.Is(err, cleanup) {
			t.Fatal(err)
		}
	}
	if resources.closes.Load() != 1 {
		t.Fatal(resources.closes.Load())
	}
	foreign := &testDIScope{}
	factory.open = func(context.Context) (di.Scope, error) { return foreign, nil }
	if _, err := execution.OpenScope(ctx, execution.ResourcesFrom(&testScopeOwner{factory, resources})); !errors.Is(err, execution.ErrScopeOwner) || foreign.closes.Load() != 1 {
		t.Fatal(err, foreign.closes.Load())
	}
}

func TestContainerContextGuardProtectsFactoryAndBorrowedScopes(t *testing.T) {
	var registry container.Registry
	if err := di.BindValue(&registry, 42); err != nil {
		t.Fatal(err)
	}
	if err := di.Bind(&registry, di.Scoped, func(ctx context.Context, resolver di.Resolver) (string, error) {
		_, err := di.Resolve[int](tenancy.WithTenant(ctx, tenancy.Default()), resolver)
		return "", err
	}, di.KeyFor[int]()); err != nil {
		t.Fatal(err)
	}
	p, err := registry.Build(container.WithContextGuard(execution.ContextGuard()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		if err := p.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	raw, err := p.NewScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := di.Resolve[string](ctx, raw); !errors.Is(err, di.ErrContextMismatch) || !errors.Is(err, execution.ErrIdentityChanged) {
		t.Fatal(err)
	}
	if _, err := execution.BorrowScope(tenancy.WithTenant(ctx, tenancy.Default()), raw); !errors.Is(err, execution.ErrIdentityChanged) {
		t.Fatal("borrow recaptured foreign security", err)
	}
	borrowed := borrowDIScope(t, ctx, raw)
	if err := raw.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.Resolve[int](ctx, borrowed); !errors.Is(err, di.ErrClosed) {
		t.Fatal(err)
	}
}

func TestContextGuardPresenceCancellationAndNonSecurityMetadata(t *testing.T) {
	base := context.Background()
	guard, err := execution.ContextGuard()(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{identity.WithPrincipal(base, identity.Principal{}), tenancy.WithTenant(base, tenancy.ID{})} {
		if err := guard(ctx); !errors.Is(err, execution.ErrIdentityChanged) {
			t.Fatal(err)
		}
	}
	if err := guard(correlation.WithID(base, correlation.ID{})); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(base)
	cancel()
	if err := guard(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := execution.ContextGuard()(nil); !errors.Is(err, execution.ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := guard(nil); !errors.Is(err, execution.ErrInvalidArgument) {
		t.Fatal(err)
	}
}
