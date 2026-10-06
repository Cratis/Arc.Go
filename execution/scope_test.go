// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

type holder struct {
	closes atomic.Int32
	close  func(context.Context) error
}

func (h *holder) Close(ctx context.Context) error {
	h.closes.Add(1)
	if h.close != nil {
		return h.close(ctx)
	}
	return nil
}
func openHolder(h *holder) execution.OpenResources {
	return func(context.Context) (execution.Resources, error) { return h, nil }
}
func mustScope(t *testing.T, ctx context.Context, h *holder) *execution.Scope {
	t.Helper()
	scope, err := execution.OpenScope(ctx, openHolder(h))
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

func TestScopeSecurityIncludesPresence(t *testing.T) {
	ctx := context.Background()
	scope := mustScope(t, ctx, &holder{})
	for _, changed := range []context.Context{
		identity.WithPrincipal(ctx, identity.Principal{}),
		tenancy.WithTenant(ctx, tenancy.ID{}),
		identity.WithPrincipal(ctx, identity.System("Admin")),
	} {
		if err := scope.CheckContext(changed); !errors.Is(err, execution.ErrIdentityChanged) {
			t.Fatalf("error = %v", err)
		}
	}
	if err := scope.CheckContext(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUseViewsExpireAndCannotClose(t *testing.T) {
	ctx := context.Background()
	h := &holder{}
	scope := mustScope(t, ctx, h)
	var retained *execution.Scope
	if err := scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
		retained = view
		if !errors.Is(view.Close(ctx), execution.ErrScopeView) {
			t.Error("callback closed view")
		}
		got, err := execution.ResourcesAs[*holder](ctx, view)
		if err != nil || got != h {
			t.Errorf("resources = %v, %v", got, err)
		}
		return view.Use(ctx, func(ctx context.Context, nested *execution.Scope) error { return nested.CheckContext(ctx) })
	}); err != nil {
		t.Fatal(err)
	}
	if err := retained.CheckContext(ctx); !errors.Is(err, execution.ErrScopeExpired) {
		t.Fatal(err)
	}
	if retained.Resources() != nil {
		t.Fatal("expired view returned resources")
	}
	if err := retained.Use(ctx, func(context.Context, *execution.Scope) error { t.Fatal("expired callback"); return nil }); !errors.Is(err, execution.ErrScopeExpired) {
		t.Fatal(err)
	}
}

func TestCloseJoinsAdmittedUsesAndStopsAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		h := &holder{}
		scope := mustScope(t, ctx, h)
		entered, release := make(chan struct{}), make(chan struct{})
		used, closed := make(chan error, 1), make(chan error, 1)
		go func() {
			used <- scope.Use(ctx, func(ctx context.Context, view *execution.Scope) error {
				close(entered)
				<-release
				return view.CheckContext(ctx)
			})
		}()
		<-entered
		go func() { closed <- scope.Close(ctx) }()
		synctest.Wait()
		if h.closes.Load() != 0 {
			t.Fatal("disposed during callback")
		}
		if err := scope.Use(ctx, func(context.Context, *execution.Scope) error { t.Fatal("admitted after close"); return nil }); !errors.Is(err, execution.ErrScopeClosed) {
			t.Fatal(err)
		}
		close(release)
		if err := <-used; err != nil {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if h.closes.Load() != 1 {
			t.Fatal("not disposed exactly once")
		}
	})
}

func TestConcurrentCloseDisposesOnceAndRecordsFailure(t *testing.T) {
	ctx := context.Background()
	failure := errors.New("dispose failed")
	h := &holder{close: func(context.Context) error { return failure }}
	scope, err := execution.OpenScope(ctx, openHolder(h))
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			if err := scope.Close(ctx); !errors.Is(err, failure) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if h.closes.Load() != 1 {
		t.Fatal(h.closes.Load())
	}
}

func TestCanceledJoinCanBeFinished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		h := &holder{}
		scope := mustScope(t, ctx, h)
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- scope.Use(ctx, func(context.Context, *execution.Scope) error { close(entered); <-release; return nil })
		}()
		<-entered
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := scope.Close(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if h.closes.Load() != 0 {
			t.Fatal("disposed while active")
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := scope.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if h.closes.Load() != 1 {
			t.Fatal("not disposed")
		}
	})
}

func TestBorrowedScopeNeverDisposesResources(t *testing.T) {
	ctx := context.Background()
	h := &holder{}
	scope, err := execution.BorrowScope(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if h.closes.Load() != 0 {
		t.Fatal("borrowed resource disposed")
	}
	if !errors.Is(scope.CheckContext(ctx), execution.ErrScopeClosed) {
		t.Fatal("borrowed wrapper still open")
	}
}
