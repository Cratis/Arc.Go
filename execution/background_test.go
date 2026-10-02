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
	"github.com/cratis/arc.go/services"
	"github.com/cratis/arc.go/tenancy"
)

type jobResource struct{ close func(context.Context) error }

func (r *jobResource) Close(ctx context.Context) error { return r.close(ctx) }
func jobProvider(t *testing.T, factory func(context.Context, *services.Scope) (*jobResource, error)) *services.Provider {
	t.Helper()
	r := &services.Registry{}
	if err := services.Bind(r, services.Scoped, factory); err != nil {
		t.Fatal(err)
	}
	p, err := r.Build()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRunExplicitMetadataFreshScopesAndNoRetention(t *testing.T) {
	closed, calls := 0, 0
	p := jobProvider(t, func(context.Context, *services.Scope) (*jobResource, error) {
		calls++
		return &jobResource{close: func(context.Context) error { closed++; return nil }}, nil
	})
	parent := identity.WithPrincipal(context.Background(), identity.System("admin"))
	parent = tenancy.WithTenant(parent, tenancy.Default())
	var previous, retained *services.Scope
	for range 2 {
		err := execution.Run(parent, p, execution.Metadata{}, 0, func(ctx context.Context, s *services.Scope) error {
			if s == previous {
				t.Error("reused scope")
			}
			previous = s
			retained = s
			if metadata := execution.Capture(ctx); metadata.Principal.IsAuthenticated() || metadata.Tenant.IsSet() || metadata.CorrelationID.IsZero() || metadata.ReceivedAt.IsZero() {
				t.Error("metadata")
			}
			_, err := services.Resolve[*jobResource](ctx, s)
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
	if _, err := services.Resolve[*jobResource](parent, retained); !errors.Is(err, services.ErrClosed) {
		t.Fatal("scope retained", err)
	}
	if err := p.Close(parent); err != nil {
		t.Fatal(err)
	}
	if closed != 2 {
		t.Fatal("scope retained in provider", closed)
	}
}
func TestRunJoinsCallbackAndCleanupErrors(t *testing.T) {
	callback := errors.New("callback")
	cleanup := errors.New("cleanup")
	p := jobProvider(t, func(context.Context, *services.Scope) (*jobResource, error) {
		return &jobResource{close: func(context.Context) error { return cleanup }}, nil
	})
	err := execution.Run(context.Background(), p, execution.Metadata{}, 0, func(ctx context.Context, s *services.Scope) error {
		if _, err := services.Resolve[*jobResource](ctx, s); err != nil {
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
func TestRunCancellationAndBoundedCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closed := false
		p := jobProvider(t, func(context.Context, *services.Scope) (*jobResource, error) {
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
		err := execution.Run(ctx, p, execution.Metadata{Principal: identity.System("jobs"), Tenant: tenancy.Default()}, 2*time.Second, func(ctx context.Context, s *services.Scope) error {
			if p, ok := identity.PrincipalFrom(ctx); !ok || !p.HasRole("jobs") {
				t.Error("system metadata")
			}
			if id, _ := tenancy.TenantFrom(ctx); id != tenancy.Default() {
				t.Error("tenant")
			}
			if _, err := services.Resolve[*jobResource](ctx, s); err != nil {
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
func TestRunDefaultCleanupTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := jobProvider(t, func(context.Context, *services.Scope) (*jobResource, error) {
			return &jobResource{close: func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) != 30*time.Second {
					t.Error("default cleanup deadline")
				}
				return nil
			}}, nil
		})
		if err := execution.Run(context.Background(), p, execution.Metadata{}, 0, func(ctx context.Context, s *services.Scope) error {
			_, err := services.Resolve[*jobResource](ctx, s)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := p.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
func TestRunCleansUpBeforeRepanicking(t *testing.T) {
	closed := false
	p := jobProvider(t, func(context.Context, *services.Scope) (*jobResource, error) {
		return &jobResource{close: func(context.Context) error { closed = true; return errors.New("cleanup failure") }}, nil
	})
	payload := &struct{ message string }{"original panic"}
	func() {
		defer func() {
			if got := recover(); got != payload || !closed {
				t.Error("panic identity or cleanup", got, closed)
			}
		}()
		_ = execution.Run(context.Background(), p, execution.Metadata{}, 0, func(ctx context.Context, s *services.Scope) error {
			if _, err := services.Resolve[*jobResource](ctx, s); err != nil {
				t.Fatal(err)
			}
			panic(payload)
		})
		t.Error("panic suppressed")
	}()
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRunInvalidArgumentsAndAlreadyCanceledContext(t *testing.T) {
	p, err := (&services.Registry{}).Build()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	call := func(context.Context, *services.Scope) error { calls++; return nil }
	for _, tc := range []struct {
		p       *services.Provider
		call    func(context.Context, *services.Scope) error
		timeout time.Duration
	}{{nil, call, 0}, {p, nil, 0}, {p, call, -1}} {
		if err := execution.Run(context.Background(), tc.p, execution.Metadata{}, tc.timeout, tc.call); !errors.Is(err, execution.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := execution.Run(ctx, p, execution.Metadata{}, 0, call); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func ExampleRun() {
	var registry services.Registry
	if err := services.BindValue(&registry, "report service"); err != nil {
		panic(err)
	}
	provider, err := registry.Build()
	if err != nil {
		panic(err)
	}
	jobCtx := context.Background()
	err = execution.Run(jobCtx, provider, execution.Metadata{Principal: identity.System("jobs"), Tenant: tenancy.Default()}, 0, func(ctx context.Context, scope *services.Scope) error {
		value, err := services.Resolve[string](ctx, scope)
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
