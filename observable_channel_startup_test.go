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

func TestShutdownJoinsChannelFactoryCleanupRetainedAfterFailedStartup(t *testing.T) {
	for _, invalidChannel := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial startup", true: "invalid channel"}[invalidChannel], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				failure := errors.New("partial startup failure")
				ready := make(chan struct{})
				cleanupCalls := 0
				holder := &observableResources{}
				builder, err := arc.NewBuilder(arc.Options{Observable: arc.ObservableOptions{CloseGrace: time.Second}, OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
				if err != nil {
					t.Fatal(err)
				}
				source := observable.FromChannelFactory(func(context.Context) (<-chan builderModel, func(context.Context) error, error) {
					cleanup := func(ctx context.Context) error {
						cleanupCalls++
						select {
						case <-ready:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					if invalidChannel {
						return nil, cleanup, nil
					}
					return make(chan builderModel), cleanup, failure
				})
				registerAppObservation(t, builder, source)
				app, err := builder.Build()
				if err != nil {
					t.Fatal(err)
				}
				if err := app.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				observation, _, err := app.Queries().(queries.ObservablePipeline).Open(context.Background(), "builderModel.Observe", queries.Request{})
				want := failure
				if invalidChannel {
					want = observable.ErrInvalidOptions
				}
				if observation != nil || !errors.Is(err, want) || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("admission = %v, %v", observation, err)
				}
				if holder.closes != 0 || cleanupCalls != 1 {
					t.Fatal("resources disposed before producer join", holder.closes, cleanupCalls)
				}
				// Failed Open returns nil, but pipeline/application retain the
				// cleanup handle and admission lease until the actual later join.
				close(ready)
				if err := app.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := app.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				if holder.closes != 1 || cleanupCalls != 2 {
					t.Fatal("cleanup lost or repeated", holder.closes, cleanupCalls)
				}
			})
		})
	}
}
