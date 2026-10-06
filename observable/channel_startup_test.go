// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observable_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/observable"
)

func TestChannelFactoryReturnsOwnedCleanupOnFailedStartup(t *testing.T) {
	for _, invalidChannel := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial startup", true: "invalid channel"}[invalidChannel], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				failure := errors.New("channel startup failed")
				ready := make(chan struct{})
				calls := 0
				var opening context.Context
				source := observable.FromChannelFactory(func(ctx context.Context) (<-chan int, func(context.Context) error, error) {
					opening = ctx
					cleanup := func(ctx context.Context) error {
						calls++
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
					return make(chan int), cleanup, failure
				})
				stream, err := source.Open(context.Background())
				want := failure
				if invalidChannel {
					want = observable.ErrInvalidOptions
				}
				if stream == nil || !errors.Is(err, want) {
					t.Fatalf("Open = %v, %v", stream, err)
				}
				if calls != 0 || opening.Err() == nil {
					t.Fatal("startup cleanup ran inline or producer was not canceled")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := stream.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				close(ready)
				if err := stream.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				if calls != 2 {
					t.Fatal("cleanup lost or repeated after joining", calls)
				}
			})
		})
	}
}
