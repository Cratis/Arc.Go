// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/services"
)

func TestProviderCleanupAttemptsEveryScopeAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &services.Registry{}
	calls := 0
	register(t, r, services.Scoped, func(context.Context, *services.Scope) (*resource, error) {
		return &resource{close: func(ctx context.Context) error { calls++; cancel(); return ctx.Err() }}, nil
	})
	register(t, r, services.Singleton, func(context.Context, *services.Scope) (*rootResource, error) {
		return &rootResource{&resource{close: func(ctx context.Context) error { calls++; return ctx.Err() }}}, nil
	})
	p, err := r.Build()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s, err := p.NewScope(ctx)
		if err != nil {
			t.Fatal(err)
		}
		resolved[*resource](t, ctx, s)
		resolved[*rootResource](t, ctx, s)
	}
	if err := p.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("skipped cleanup", calls)
	}
	if err := p.Close(context.Background()); !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatal(err, calls)
	}
}
func TestProviderCloseJoinsScopeCleanupBeforeRoot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &services.Registry{}
		release := make(chan struct{})
		scopeClosed := false
		register(t, r, services.Scoped, func(context.Context, *services.Scope) (*resource, error) {
			return &resource{close: func(context.Context) error { <-release; scopeClosed = true; return nil }}, nil
		})
		register(t, r, services.Singleton, func(context.Context, *services.Scope) (*rootResource, error) {
			return &rootResource{&resource{close: func(context.Context) error {
				if !scopeClosed {
					t.Error("root closed before scope")
				}
				return nil
			}}}, nil
		})
		p, err := r.Build()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		s, err := p.NewScope(ctx)
		if err != nil {
			t.Fatal(err)
		}
		resolved[*resource](t, ctx, s)
		resolved[*rootResource](t, ctx, s)
		scopeResult := make(chan error, 1)
		go func() { scopeResult <- s.Close(ctx) }()
		synctest.Wait()
		closeCtx, cancel := context.WithCancel(ctx)
		providerResult := make(chan error, 1)
		go func() { providerResult <- p.Close(closeCtx) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case err := <-providerResult:
			t.Fatal("provider abandoned scope cleanup", err)
		default:
		}
		close(release)
		if err := <-scopeResult; err != nil {
			t.Fatal(err)
		}
		if err := <-providerResult; err != nil {
			t.Fatal(err)
		}
	})
}
func TestNewScopeRacesProviderCloseAndCloseRunsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &services.Registry{}
		var calls atomic.Int64
		register(t, r, services.Singleton, func(context.Context, *services.Scope) (*rootResource, error) {
			return &rootResource{&resource{close: func(context.Context) error { calls.Add(1); return nil }}}, nil
		})
		p, err := r.Build()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		s, err := p.NewScope(ctx)
		if err != nil {
			t.Fatal(err)
		}
		resolved[*rootResource](t, ctx, s)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 40 {
			wg.Go(func() {
				<-start
				child, err := p.NewScope(ctx)
				if err != nil {
					if !errors.Is(err, services.ErrClosed) {
						t.Error(err)
					}
					return
				}
				if !p.Owns(child) {
					t.Error("wrong owner")
				}
				if err := child.Close(ctx); err != nil {
					t.Error(err)
				}
			})
		}
		for range 10 {
			wg.Go(func() {
				<-start
				if err := p.Close(ctx); err != nil {
					t.Error(err)
				}
			})
		}
		close(start)
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatal(calls.Load())
		}
		if _, err := p.NewScope(ctx); !errors.Is(err, services.ErrClosed) {
			t.Fatal(err)
		}
	})
}
