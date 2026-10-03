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

func TestCloseJoinsInFlightSubscriberClone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		calls := 0
		state := mustState(t, 1, observable.SubjectOptions[int]{Clone: func(value int) (int, error) {
			calls++
			if calls == 2 {
				close(entered)
				<-release
			}
			return value, nil
		}})
		stream := mustOpen(t, state)
		nextDone := make(chan error, 1)
		go func() { _, err := stream.Next(t.Context()); nextDone <- err }()
		<-entered
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := stream.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unjoined clone close = %v", err)
		}
		close(release)
		if err := stream.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-nextDone; err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("clone calls = %d", calls)
		}
		if _, err := stream.Next(t.Context()); !errors.Is(err, observable.ErrClosed) {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatal("clone callback after joined Close")
		}
	})
}

func TestChannelCleanupCanContinueAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		values, release := make(chan int), make(chan struct{})
		calls := 0
		source := observable.FromChannelFactory(func(context.Context) (<-chan int, func(context.Context) error, error) {
			return values, func(ctx context.Context) error {
				calls++
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, nil
		})
		stream := mustOpen(t, source)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := stream.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		close(release)
		if err := stream.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("cleanup attempts = %d", calls)
		}
	})
}

func TestProducerPanicIsReportedAndJoined(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := observable.FromProducer(func(context.Context, func(int) error) error { panic("private failure") })
		stream, err := source.Open(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Next(t.Context()); !errors.Is(err, observable.ErrProducerPanic) {
			t.Fatal(err)
		}
		if err := stream.Close(t.Context()); !errors.Is(err, observable.ErrProducerPanic) {
			t.Fatal(err)
		}
		if err := stream.Close(t.Context()); !errors.Is(err, observable.ErrProducerPanic) {
			t.Fatal(err)
		}
	})
}
