// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func TestShutdownRetainsExplicitJoinAfterPartialResourceStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("partial resource startup failed")
		resources := &appJoinResources{ready: make(chan struct{})}
		builder, err := arc.NewBuilder(arc.Options{Observable: arc.ObservableOptions{CloseGrace: time.Second}, OpenResources: func(context.Context) (execution.Resources, error) { return resources, failure }})
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
		var pending *execution.PendingScopeError
		if observation != nil || !errors.Is(err, failure) || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &pending) {
			t.Fatalf("admission = %v, %v", observation, err)
		}
		if resources.closes.Load() != 1 {
			t.Fatal("startup disposal repeated")
		}
		close(resources.ready)
		if err := app.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := pending.Scope().Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if resources.closes.Load() != 1 {
			t.Fatal("disposal repeated during shutdown")
		}
	})
}
