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

type diagnosticJoinHolder struct {
	closes int
	joins  int
	join   func() error
}

func (h *diagnosticJoinHolder) Close(context.Context) error { h.closes++; return nil }
func (h *diagnosticJoinHolder) Join(context.Context) error {
	h.joins++
	if h.joins == 1 {
		return h.join()
	}
	return nil
}

type typedJoinFailure struct{ cause error }

func (e *typedJoinFailure) Error() string { return "typed join failure" }
func (e *typedJoinFailure) Unwrap() error { return e.cause }

func TestResourceJoinRetainsPriorDiagnosticsWithoutWaitErrors(t *testing.T) {
	failure := errors.New("cleanup domain failure")
	typed := &typedJoinFailure{cause: errors.Join(failure, context.DeadlineExceeded)}
	for _, tc := range []struct {
		name  string
		first func() error
		want  error
		panic bool
	}{
		{name: "plain timeout", first: func() error { return context.DeadlineExceeded }},
		{name: "mixed failure", first: func() error { return errors.Join(failure, context.DeadlineExceeded) }, want: failure},
		{name: "typed mixed failure", first: func() error { return typed }, want: typed},
		{name: "panic", first: func() error { panic("join diagnostic") }, panic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &diagnosticJoinHolder{join: tc.first}
			scope, err := execution.OpenScope(context.Background(), func(context.Context) (execution.Resources, error) { return h, nil })
			if err != nil {
				t.Fatal(err)
			}
			first := scope.Close(context.Background())
			var pending *execution.PendingScopeError
			if !errors.Is(first, execution.ErrScopeJoinPending) || !errors.As(first, &pending) || pending.Scope() != scope {
				t.Fatal("missing pending owner", first)
			}
			for range 2 {
				err = scope.Close(context.Background())
				if (!tc.panic && !errors.Is(err, tc.want)) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, execution.ErrScopeJoinPending) {
					t.Fatal("lost failure or retained provisional wait", err)
				}
				var diagnostic *execution.PanicError
				if errors.As(err, &diagnostic) != tc.panic {
					t.Fatal("panic diagnostic lost", err)
				}
				if tc.want == typed {
					var got *typedJoinFailure
					if !errors.As(err, &got) || got != typed || !errors.Is(err, failure) {
						t.Fatal("typed mixed failure lost", err)
					}
				}
			}
			if h.closes != 1 || h.joins != 2 {
				t.Fatalf("closes/joins = %d/%d", h.closes, h.joins)
			}
		})
	}
}

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
