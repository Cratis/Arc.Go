// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
)

func ExampleRunWithResources() {
	h := &holder{}
	err := execution.RunWithResources(context.Background(), openHolder(h), execution.Metadata{}, 0,
		func(ctx context.Context, scope *execution.Scope) error {
			resources, err := execution.ResourcesAs[*holder](ctx, scope)
			if err != nil {
				return err
			}
			fmt.Println("closes during callback:", resources.closes.Load())
			return nil
		})
	fmt.Println("error:", err)
	fmt.Println("closes after callback:", h.closes.Load())
	// Output:
	// closes during callback: 0
	// error: <nil>
	// closes after callback: 1
}

type otherHolder struct{}

func (*otherHolder) Close(context.Context) error { return nil }

func TestResourceTypesAndInvalidScopes(t *testing.T) {
	ctx := context.Background()
	for _, scope := range []*execution.Scope{nil, {}} {
		if !errors.Is(scope.CheckContext(ctx), execution.ErrInvalidScope) {
			t.Fatal("accepted invalid scope")
		}
	}
	if _, err := execution.BorrowScope(ctx, (*holder)(nil)); !errors.Is(err, execution.ErrResourceType) {
		t.Fatal(err)
	}
	if _, err := execution.OpenScope(ctx, openHolder(nil)); !errors.Is(err, execution.ErrResourceType) {
		t.Fatal(err)
	}
	scope := mustScope(t, ctx, &holder{})
	if _, err := execution.ResourcesAs[*otherHolder](ctx, scope); !errors.Is(err, execution.ErrResourceType) {
		t.Fatal(err)
	}
	for _, open := range []execution.OpenResources{nil, func(context.Context) (execution.Resources, error) { return nil, nil }} {
		empty, err := execution.OpenScope(ctx, open)
		if err != nil {
			t.Fatal(err)
		}
		if empty.Resources() != nil {
			t.Fatal("nonempty")
		}
		if err := empty.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenFailureJoinsCleanupAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	openErr, closeErr := errors.New("open failed"), errors.New("close failed")
	h := &holder{close: func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Error("canceled cleanup")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded cleanup")
		}
		return closeErr
	}}
	scope, err := execution.OpenScope(ctx, func(context.Context) (execution.Resources, error) { cancel(); return h, openErr })
	if scope != nil || !errors.Is(err, openErr) || !errors.Is(err, closeErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("scope/error = %v, %v", scope, err)
	}
	if h.closes.Load() != 1 {
		t.Fatal("leaked failed open")
	}
}

type switchContext struct {
	context.Context
	current context.Context
}

func (c *switchContext) Value(key any) any { return c.current.Value(key) }

func TestOpeningRechecksCapturedSecurityBeforePublishing(t *testing.T) {
	base := context.Background()
	ctx := &switchContext{Context: base, current: base}
	h := &holder{}
	scope, err := execution.OpenScope(ctx, func(context.Context) (execution.Resources, error) {
		ctx.current = identity.WithPrincipal(base, identity.Principal{})
		return h, nil
	})
	if scope != nil || !errors.Is(err, execution.ErrIdentityChanged) || h.closes.Load() != 1 {
		t.Fatalf("scope/error/closes = %v/%v/%d", scope, err, h.closes.Load())
	}
}

func TestRepeatedCloseReturnsRecordedOutcomeEvenWithCanceledContext(t *testing.T) {
	ctx := context.Background()
	failure := errors.New("cleanup")
	h := &holder{close: func(context.Context) error { return failure }}
	scope, err := execution.OpenScope(ctx, openHolder(h))
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(ctx); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := scope.Close(canceled); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if h.closes.Load() != 1 {
		t.Fatal("repeated cleanup")
	}
}

func TestRunWithResourcesInstallsMetadataAndDisposesAfterCallback(t *testing.T) {
	ctx := identity.WithPrincipal(context.Background(), identity.System("Admin"))
	h := &holder{}
	failure := errors.New("callback failed")
	err := execution.RunWithResources(ctx, openHolder(h), execution.Metadata{}, 0, func(ctx context.Context, view *execution.Scope) error {
		principal, present := identity.PrincipalFrom(ctx)
		if !present || principal.IsAuthenticated() {
			t.Error("inherited parent authority")
		}
		if h.closes.Load() != 0 {
			t.Error("closed before callback")
		}
		if !errors.Is(view.Close(ctx), execution.ErrScopeView) {
			t.Error("closing view")
		}
		return failure
	})
	if !errors.Is(err, failure) || h.closes.Load() != 1 {
		t.Fatalf("error/closes = %v/%d", err, h.closes.Load())
	}
}
