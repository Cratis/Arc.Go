// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

type observationResources struct {
	ready  chan struct{}
	closes atomic.Int32
}

func (r *observationResources) Close(context.Context) error { r.closes.Add(1); return nil }
func (r *observationResources) Join(ctx context.Context) error {
	select {
	case <-r.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type failedObservationResources struct {
	closes  atomic.Int32
	failure error
}

func (r *failedObservationResources) Close(context.Context) error { r.closes.Add(1); return r.failure }

func TestCloseObservationsRetriesOnlyExplicitResourceJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewState(Item{Name: "first"}, observable.SubjectOptions[Item]{})
		mustRegister(t, err)
		resources := &observationResources{ready: make(chan struct{})}
		pipeline := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
		observation, _, err := pipeline.Open(context.Background(), "Item.Observe", queries.Request{})
		mustRegister(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := observation.Close(ctx); !errors.Is(err, execution.ErrScopeJoinPending) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		close(resources.ready)
		mustRegister(t, pipeline.CloseObservations(context.Background()))
		mustRegister(t, observation.Close(context.Background()))
		if resources.closes.Load() != 1 {
			t.Fatal("repeated disposal", resources.closes.Load())
		}
	})
}

func TestCompletedResourceFailureDoesNotRetainObservationForever(t *testing.T) {
	for _, failure := range []error{errors.New("completed cleanup failed"), context.DeadlineExceeded} {
		state, err := observable.NewState(Item{}, observable.SubjectOptions[Item]{})
		mustRegister(t, err)
		resources := &failedObservationResources{failure: failure}
		pipeline := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{MaxObservations: 1, OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
		observation, _, err := pipeline.Open(context.Background(), "Item.Observe", queries.Request{})
		mustRegister(t, err)
		if err := observation.Close(context.Background()); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		// No explicit Join contract: the recorded disposal failure is final, so
		// capacity is freed without repeating any disposal side effect.
		next, _, err := pipeline.Open(context.Background(), "Item.Observe", queries.Request{})
		mustRegister(t, err)
		if next == nil {
			t.Fatal("completed failure leaked observation capacity")
		}
		if err := next.Close(context.Background()); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		mustRegister(t, pipeline.CloseObservations(context.Background()))
		if resources.closes.Load() != 2 {
			t.Fatal("disposal was retried", resources.closes.Load())
		}
	}
}
