// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observable_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
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

func TestChannelCleanupPanicCanContinueWithoutRepeatingDisposal(t *testing.T) {
	ready := make(chan struct{})
	calls, initiations := 0, 0
	source := observable.FromChannelFactory(func(context.Context) (<-chan int, func(context.Context) error, error) {
		return make(chan int), func(context.Context) error {
			calls++
			if initiations == 0 {
				initiations++
				panic("cleanup before producer join")
			}
			<-ready
			return nil
		}, nil
	})
	stream, err := source.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic *execution.PanicError
	err = stream.Close(context.Background())
	if !errors.Is(err, observable.ErrJoinPending) || !errors.As(err, &diagnostic) || diagnostic.Value != "cleanup before producer join" {
		t.Fatal("unknown completion lost ownership or diagnostic", err)
	}
	close(ready)
	for range 2 {
		err = stream.Close(context.Background())
		if errors.Is(err, observable.ErrJoinPending) || !errors.As(err, &diagnostic) {
			t.Fatal("completed join lost diagnostic or retained pending", err)
		}
	}
	if calls != 2 || initiations != 1 {
		t.Fatalf("attempts/initiations = %d/%d", calls, initiations)
	}
}

func TestCompletedChannelCleanupFailureIsCached(t *testing.T) {
	failure := errors.New("joined producer cleanup failed")
	calls := 0
	source := observable.FromChannelFactory(func(context.Context) (<-chan int, func(context.Context) error, error) {
		return make(chan int), func(context.Context) error { calls++; return failure }, nil
	})
	stream, err := source.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := stream.Close(context.Background()); !errors.Is(err, failure) || errors.Is(err, observable.ErrJoinPending) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("completed cleanup repeated", calls)
	}
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
