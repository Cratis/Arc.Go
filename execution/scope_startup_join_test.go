// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
)

func TestRunWithResourcesRetainsPendingScopeAfterSuccessfulOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		holder := &joinHolder{ready: make(chan struct{})}
		failure := errors.New("unary callback failed")
		err := execution.RunWithResources(context.Background(), func(context.Context) (execution.Resources, error) {
			return holder, nil
		}, execution.Metadata{}, time.Second, func(context.Context, *execution.Scope) error { return failure })
		var pending *execution.PendingScopeError
		if !errors.Is(err, failure) || !errors.Is(err, execution.ErrScopeJoinPending) || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &pending) {
			t.Fatal("unary cleanup ownership hidden", err)
		}
		// Another incomplete attempt is independently inspectable, without
		// storing a PendingScopeError in the scope's own cached diagnostics.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		again := pending.Scope().Close(ctx)
		var next *execution.PendingScopeError
		if !errors.As(again, &next) || next.Scope() != pending.Scope() || !errors.Is(again, context.DeadlineExceeded) {
			t.Fatal("repeated pending result lost scope", again)
		}
		close(holder.ready)
		if err := next.Scope().Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := next.Scope().Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if holder.closes.Load() != 1 || holder.joins.Load() != 3 {
			t.Fatal("unary disposal repeated")
		}
	})
}

func TestFailedResourceOpeningRetainsPendingCleanupInItsError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("partial resource startup failure")
		holder := &joinHolder{ready: make(chan struct{})}
		scope, err := execution.OpenScope(context.Background(), func(context.Context) (execution.Resources, error) { return holder, failure })
		var pending *execution.PendingScopeError
		if scope != nil || !errors.Is(err, failure) || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &pending) {
			t.Fatalf("scope/error = %v, %v", scope, err)
		}
		if !errors.Is(pending, failure) || !errors.Is(pending, execution.ErrScopeJoinPending) {
			t.Fatal("ownership-bearing error lost opening diagnostics", pending)
		}
		if holder.closes.Load() != 1 || holder.joins.Load() != 1 {
			t.Fatal("startup disposal not initiated once")
		}
		if err := pending.Scope().CheckContext(context.Background()); !errors.Is(err, execution.ErrScopeClosed) {
			t.Fatal("failed scope admitted work", err)
		}
		close(holder.ready)
		if err := pending.Scope().Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := pending.Scope().Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if holder.closes.Load() != 1 || holder.joins.Load() != 2 {
			t.Fatal("startup disposal repeated")
		}
	})
}
