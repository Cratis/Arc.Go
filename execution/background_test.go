// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

type jobResource struct{ close func(context.Context) error }

func (r *jobResource) Close(ctx context.Context) error { return r.close(ctx) }
func jobProvider(t *testing.T, factory func(context.Context, di.Resolver) (*jobResource, error)) di.Provider {
	t.Helper()
	r := &container.Registry{}
	if err := di.Bind(r, di.Scoped, factory); err != nil {
		t.Fatal(err)
	}
	p, err := r.Build(container.WithContextGuard(execution.ContextGuard()))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRunWithResourcesExplicitMetadataFreshScopesAndNoRetention(t *testing.T) {
	closed, calls := 0, 0
	p := jobProvider(t, func(context.Context, di.Resolver) (*jobResource, error) {
		calls++
		return &jobResource{close: func(context.Context) error { closed++; return nil }}, nil
	})
	parent := identity.WithPrincipal(context.Background(), identity.System("admin"))
	parent = tenancy.WithTenant(parent, tenancy.Default())
	var previous, retained *execution.Scope
	for range 2 {
		err := execution.RunWithResources(parent, execution.ResourcesFrom(p), execution.Metadata{}, 0, func(ctx context.Context, s *execution.Scope) error {
			if s == previous {
				t.Error("reused scope")
			}
			previous = s
			retained = s
			if metadata := execution.Capture(ctx); metadata.Principal.IsAuthenticated() || metadata.Tenant.IsSet() || metadata.CorrelationID.IsZero() || metadata.ReceivedAt.IsZero() {
				t.Error("metadata")
			}
			_, err := execution.Resolve[*jobResource](ctx, s)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if closed != calls {
			t.Fatal("cleanup not synchronous")
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	if _, err := execution.Resolve[*jobResource](context.Background(), retained); !errors.Is(err, execution.ErrScopeExpired) {
		t.Fatal("scope retained", err)
	}
	if err := p.Close(parent); err != nil {
		t.Fatal(err)
	}
	if closed != 2 {
		t.Fatal("scope retained in provider", closed)
	}
}
func TestRunWithResourcesJoinsCallbackAndCleanupErrors(t *testing.T) {
	callback := errors.New("callback")
	cleanup := errors.New("cleanup")
	p := jobProvider(t, func(context.Context, di.Resolver) (*jobResource, error) {
		return &jobResource{close: func(context.Context) error { return cleanup }}, nil
	})
	err := execution.RunWithResources(context.Background(), execution.ResourcesFrom(p), execution.Metadata{}, 0, func(ctx context.Context, s *execution.Scope) error {
		if _, err := execution.Resolve[*jobResource](ctx, s); err != nil {
			return err
		}
		return callback
	})
	if !errors.Is(err, callback) || !errors.Is(err, cleanup) {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRunWithResourcesCancellationAndBoundedCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closed := false
		p := jobProvider(t, func(context.Context, di.Resolver) (*jobResource, error) {
			return &jobResource{close: func(ctx context.Context) error {
				if ctx.Err() != nil {
					t.Error("cleanup inherited cancellation")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) != 2*time.Second {
					t.Error("budget", deadline)
				}
				<-ctx.Done()
				closed = true
				return ctx.Err()
			}}, nil
		})
		err := execution.RunWithResources(ctx, execution.ResourcesFrom(p), execution.Metadata{Principal: identity.System("jobs"), Tenant: tenancy.Default()}, 2*time.Second, func(ctx context.Context, s *execution.Scope) error {
			if p, ok := identity.PrincipalFrom(ctx); !ok || !p.HasRole("jobs") {
				t.Error("system metadata")
			}
			if id, _ := tenancy.TenantFrom(ctx); id != tenancy.Default() {
				t.Error("tenant")
			}
			if _, err := execution.Resolve[*jobResource](ctx, s); err != nil {
				return err
			}
			cancel()
			if ctx.Err() != context.Canceled {
				t.Error("cancellation lost")
			}
			return nil
		})
		if !closed || !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(closed, err)
		}
		if err := p.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
func TestRunWithResourcesDefaultCleanupTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := jobProvider(t, func(context.Context, di.Resolver) (*jobResource, error) {
			return &jobResource{close: func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) != 30*time.Second {
					t.Error("default cleanup deadline")
				}
				return nil
			}}, nil
		})
		if err := execution.RunWithResources(context.Background(), execution.ResourcesFrom(p), execution.Metadata{}, 0, func(ctx context.Context, s *execution.Scope) error {
			_, err := execution.Resolve[*jobResource](ctx, s)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := p.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
func TestRunWithResourcesCleansUpPanicAndJoinsDiagnostics(t *testing.T) {
	closed := false
	cleanup := errors.New("cleanup failure")
	p := jobProvider(t, func(context.Context, di.Resolver) (*jobResource, error) {
		return &jobResource{close: func(context.Context) error { closed = true; return cleanup }}, nil
	})
	payload := &struct{ message string }{"original panic"}
	err := execution.RunWithResources(context.Background(), execution.ResourcesFrom(p), execution.Metadata{}, 0, func(ctx context.Context, s *execution.Scope) error {
		if _, err := execution.Resolve[*jobResource](ctx, s); err != nil {
			return err
		}
		panic(payload)
	})
	var diagnostic *execution.PanicError
	if !closed || !errors.As(err, &diagnostic) || diagnostic.Value != payload || !errors.Is(err, cleanup) {
		t.Fatal("panic identity or cleanup", err, closed)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRunWithResourcesInvalidArgumentsAndAlreadyCanceledContext(t *testing.T) {
	calls := 0
	call := func(context.Context, *execution.Scope) error { calls++; return nil }
	for _, tc := range []struct {
		ctx     context.Context
		call    func(context.Context, *execution.Scope) error
		timeout time.Duration
	}{{nil, call, 0}, {context.Background(), nil, 0}, {context.Background(), call, -1}} {
		if err := execution.RunWithResources(tc.ctx, nil, execution.Metadata{}, tc.timeout, tc.call); !errors.Is(err, execution.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := execution.RunWithResources(ctx, nil, execution.Metadata{}, 0, call); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
}
func ExampleResourcesFrom() {
	var registry container.Registry
	if err := di.BindValue(&registry, "report service"); err != nil {
		panic(err)
	}
	provider, err := registry.Build(container.WithContextGuard(execution.ContextGuard()))
	if err != nil {
		panic(err)
	}
	jobCtx := context.Background()
	err = execution.RunWithResources(jobCtx, execution.ResourcesFrom(provider), execution.Metadata{Principal: identity.System("jobs"), Tenant: tenancy.Default()}, 0, func(ctx context.Context, scope *execution.Scope) error {
		value, err := execution.Resolve[string](ctx, scope)
		if err != nil {
			return err
		}
		principal, _ := identity.PrincipalFrom(ctx)
		tenant, _ := tenancy.TenantFrom(ctx)
		fmt.Println(value, principal.ID(), tenant)
		return nil
	})
	if err != nil {
		panic(err)
	}
	if err := provider.Close(jobCtx); err != nil {
		panic(err)
	}
	// Output: report service [System] Default
}
