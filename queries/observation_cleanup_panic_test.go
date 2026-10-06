// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func TestFailedChannelStartupRetainsCleanupPanicUntilProducerJoins(t *testing.T) {
	for _, rawStream := range []bool{false, true} {
		t.Run(map[bool]string{false: "channel factory", true: "custom stream boundary"}[rawStream], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				failure := errors.New("partial channel startup failed")
				started, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
				go func() {
					defer close(joined)
					close(started)
					<-release
				}()
				<-started
				calls := 0
				cleanup := func(ctx context.Context) error {
					calls++
					if calls == 1 {
						panic("cleanup interrupted before join")
					}
					select {
					case <-joined:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				source := observable.FromChannelFactory(func(context.Context) (<-chan Item, func(context.Context) error, error) {
					return make(chan Item), cleanup, failure
				})
				if rawStream {
					source = sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) {
						return streamFuncs[Item]{next: func(context.Context) (Item, error) { return Item{}, io.EOF }, close: cleanup}, failure
					})
				}
				holder := &plainResources{}
				p := observationPipeline(t, observableRegistry(t, source), queries.PipelineOptions{CleanupTimeout: time.Second, MaxObservations: 1, OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
				o, _, err := p.Open(context.Background(), "Item.Observe", queries.Request{})
				var diagnostic *execution.PanicError
				if o != nil || !errors.Is(err, failure) || !errors.Is(err, observable.ErrJoinPending) || !errors.As(err, &diagnostic) || calls != 1 || holder.closes != 0 {
					t.Fatalf("failed startup = %v, %v; cleanup/resource calls %d/%d", o, err, calls, holder.closes)
				}
				if _, _, err := p.Open(context.Background(), "Item.Observe", queries.Request{}); !errors.Is(err, queries.ErrObservationCapacity) {
					t.Fatal("unknown join released capacity", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := p.CloseObservations(ctx); !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &diagnostic) {
					t.Fatal("incomplete continuation lost panic", err)
				}
				if holder.closes != 0 {
					t.Fatal("resources disposed beneath producer")
				}
				close(release)
				<-joined
				err = p.CloseObservations(context.Background())
				if !errors.As(err, &diagnostic) || errors.Is(err, observable.ErrJoinPending) || errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("joined cleanup lost panic or retained waiting", err)
				}
				if err := p.CloseObservations(context.Background()); err != nil {
					t.Fatal("joined observation not forgotten", err)
				}
				if holder.closes != 1 || calls != 3 {
					t.Fatalf("cleanup/resource calls %d/%d", calls, holder.closes)
				}
			})
		})
	}
}
