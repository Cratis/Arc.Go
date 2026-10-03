// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
)

type joinHolder struct {
	closes  atomic.Int32
	joins   atomic.Int32
	ready   chan struct{}
	failure error
}

func (h *joinHolder) Close(context.Context) error { h.closes.Add(1); return h.failure }
func (h *joinHolder) Join(ctx context.Context) error {
	h.joins.Add(1)
	select {
	case <-h.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestResourceJoinResumesWithoutRepeatingDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &joinHolder{ready: make(chan struct{})}
		scope, err := execution.OpenScope(context.Background(), func(context.Context) (execution.Resources, error) { return h, nil })
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := scope.Close(ctx); !errors.Is(err, execution.ErrScopeJoinPending) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if err := scope.CheckContext(context.Background()); !errors.Is(err, execution.ErrScopeClosed) {
			t.Fatal("admission reopened", err)
		}
		close(h.ready)
		if err := scope.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := scope.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if h.closes.Load() != 1 || h.joins.Load() != 2 {
			t.Fatalf("closes/joins = %d/%d", h.closes.Load(), h.joins.Load())
		}
	})
}

func TestCompletedResourceFailuresAreNeverRetried(t *testing.T) {
	for _, failure := range []error{errors.New("nonretryable disposal failure"), context.DeadlineExceeded} {
		h := &holder{close: func(context.Context) error { return failure }}
		scope, err := execution.OpenScope(context.Background(), openHolder(h))
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			err := scope.Close(context.Background())
			if !errors.Is(err, failure) || errors.Is(err, execution.ErrScopeJoinPending) {
				t.Fatal(err)
			}
		}
		if h.closes.Load() != 1 {
			t.Fatal("repeated nonretryable cleanup", h.closes.Load())
		}
	}
}

func TestResourceJoinPreservesInitiationFailureAlongsideTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("disposal side effect failed")
		h := &joinHolder{ready: make(chan struct{}), failure: errors.Join(failure, context.DeadlineExceeded)}
		scope, err := execution.OpenScope(context.Background(), func(context.Context) (execution.Resources, error) { return h, nil })
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := scope.Close(ctx); !errors.Is(err, failure) || !errors.Is(err, execution.ErrScopeJoinPending) {
			t.Fatal(err)
		}
		close(h.ready)
		for range 2 {
			err := scope.Close(context.Background())
			if !errors.Is(err, failure) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, execution.ErrScopeJoinPending) {
				t.Fatal(err)
			}
		}
		if h.closes.Load() != 1 || h.joins.Load() != 2 {
			t.Fatal("repeated disposal")
		}
	})
}
