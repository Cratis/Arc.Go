// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

type appJoinResources struct {
	ready  chan struct{}
	closes atomic.Int32
}

func (r *appJoinResources) Close(context.Context) error { r.closes.Add(1); return nil }
func (r *appJoinResources) Join(ctx context.Context) error {
	select {
	case <-r.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestShutdownCanResumeResourceJoinWithoutRepeatingDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resources := &appJoinResources{ready: make(chan struct{})}
		builder, err := arc.NewBuilder(arc.Options{OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
		if err != nil {
			t.Fatal(err)
		}
		state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
		if err != nil {
			t.Fatal(err)
		}
		registerAppObservation(t, builder, state)
		app, err := builder.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		observation, _, err := app.Queries().(queries.ObservablePipeline).Open(context.Background(), "builderModel.Observe", queries.Request{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		close(resources.ready)
		if err := app.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := observation.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if resources.closes.Load() != 1 {
			t.Fatal("repeated disposal", resources.closes.Load())
		}
	})
}
