// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/execution"
)

func TestFailedResourceOpeningRetainsPendingCleanupInItsError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("partial resource startup failure")
		holder := &joinHolder{ready: make(chan struct{})}
		scope, err := execution.OpenScope(context.Background(), func(context.Context) (execution.Resources, error) { return holder, failure })
		var pending *execution.PendingScopeError
		if scope != nil || !errors.Is(err, failure) || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &pending) {
			t.Fatalf("scope/error = %v, %v", scope, err)
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
