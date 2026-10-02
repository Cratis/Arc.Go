// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/services"
)

func TestCreatorCancellationDoesNotFailLiveWaiter(t *testing.T) {
	for _, lifetime := range []services.Lifetime{services.Singleton, services.Scoped} {
		t.Run(map[services.Lifetime]string{services.Singleton: "singleton", services.Scoped: "scoped"}[lifetime], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				registry := &services.Registry{}
				entered, release := make(chan struct{}), make(chan struct{})
				calls := 0
				register(t, registry, lifetime, func(ctx context.Context, _ *services.Scope) (*item, error) {
					calls++
					if calls == 1 {
						close(entered)
						<-release
						return nil, ctx.Err()
					}
					return &item{number: 42}, nil
				})
				p := provider(t, registry)
				ctx := context.Background()
				creatorScope := scope(t, p, ctx)
				waiterScope := creatorScope
				if lifetime == services.Singleton {
					waiterScope = scope(t, p, ctx)
				}
				creatorCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				creator := make(chan error, 1)
				go func() { _, err := services.Resolve[*item](creatorCtx, creatorScope); creator <- err }()
				<-entered
				type result struct {
					value *item
					err   error
				}
				waiter := make(chan result, 1)
				go func() { value, err := services.Resolve[*item](ctx, waiterScope); waiter <- result{value, err} }()
				synctest.Wait() // Both creator and waiter must be blocked on the first attempt.
				if calls != 1 {
					t.Fatal("waiter did not join the attempt", calls)
				}
				cancel()
				close(release)
				if err := <-creator; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				got := <-waiter
				if got.err != nil || got.value == nil || got.value.number != 42 || calls != 2 {
					t.Fatalf("waiter value=%v err=%v calls=%d", got.value, got.err, calls)
				}
				if cached := resolved[*item](t, ctx, waiterScope); cached != got.value || calls != 2 {
					t.Fatal("retry not cached")
				}
			})
		})
	}
}

func TestFailedValueCleanupHasIndependentBoundedContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		registry := &services.Registry{}
		closed := 0
		factoryFailure := errors.New("factory error")
		cleanupFailure := errors.New("cleanup error")
		register(t, registry, services.Scoped, func(context.Context, *services.Scope) (*resource, error) {
			cancel()
			return &resource{close: func(cleanupCtx context.Context) error {
				closed++
				if cleanupCtx.Err() != nil {
					t.Error("cleanup inherited creator cancellation")
				}
				deadline, ok := cleanupCtx.Deadline()
				if !ok || deadline.Sub(time.Now()) != 30*time.Second {
					t.Error("missing 30s cleanup budget")
				}
				return cleanupFailure
			}}, factoryFailure
		})
		p := provider(t, registry)
		s := scope(t, p, ctx)
		_, err := services.Resolve[*resource](ctx, s)
		if !errors.Is(err, context.Canceled) || !errors.Is(err, factoryFailure) || !errors.Is(err, cleanupFailure) || closed != 1 {
			t.Fatalf("cleanup=%d error=%v", closed, err)
		}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if closed != 1 {
			t.Fatal("failed value cleaned twice")
		}
	})
}
