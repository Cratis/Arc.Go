// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observable_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/observable"
)

func mustState[T any](t *testing.T, value T, options observable.SubjectOptions[T]) *observable.State[T] {
	t.Helper()
	s, err := observable.NewState(value, options)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func mustOpen[T any](t *testing.T, source observable.Source[T]) observable.Stream[T] {
	t.Helper()
	s, err := source.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}
func next[T any](t *testing.T, s observable.Stream[T], want T) {
	t.Helper()
	got, err := s.Next(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Next = %v, %v; want %v", got, err, want)
	}
}

func TestPendingCurrentNilAndZero(t *testing.T) {
	state, err := observable.NewPendingState[*int](observable.SubjectOptions[*int]{})
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := state.Current(t.Context()); present || err != nil {
		t.Fatalf("pending = %v, %v", present, err)
	}
	s := mustOpen(t, state)
	if err := state.Publish(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	next(t, s, (*int)(nil))
	if value, present, err := state.Current(t.Context()); value != nil || !present || err != nil {
		t.Fatalf("current nil = %v, %v, %v", value, present, err)
	}
	zero := mustState(t, 0, observable.SubjectOptions[int]{})
	next(t, mustOpen(t, zero), 0)
}

func TestCompleteRetainsCurrentAndDrainsQueue(t *testing.T) {
	state := mustState(t, 1, observable.SubjectOptions[int]{})
	s := mustOpen(t, state)
	if err := state.Publish(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if err := state.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := state.Complete(); err != nil {
		t.Fatal(err)
	}
	next(t, s, 1)
	next(t, s, 2)
	if _, err := s.Next(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if value, present, err := state.Current(t.Context()); value != 2 || !present || err != nil {
		t.Fatalf("completed current = %v, %v, %v", value, present, err)
	}
	late := mustOpen(t, state)
	next(t, late, 2)
	if _, err := late.Next(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if err := state.Publish(t.Context(), 3); !errors.Is(err, observable.ErrClosed) {
		t.Fatal(err)
	}
}

func TestFailureNeverPresentsStaleSuccess(t *testing.T) {
	state := mustState(t, 1, observable.SubjectOptions[int]{})
	s := mustOpen(t, state)
	failure := errors.New("source failure")
	if err := state.Fail(failure); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.Current(t.Context()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := s.Next(t.Context()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := state.Open(t.Context()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := state.Publish(t.Context(), 2); !errors.Is(err, observable.ErrClosed) {
		t.Fatal(err)
	}
}

func TestOverflowIsIsolatedAndReportedAsPartial(t *testing.T) {
	subject, err := observable.NewSubject[int](observable.SubjectOptions[int]{Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	slow, fast := mustOpen(t, subject), mustOpen(t, subject)
	if err := subject.Publish(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	next(t, fast, 1)
	var report *observable.PublishError
	if err := subject.Publish(t.Context(), 2); !errors.As(err, &report) || report.Delivered != 1 || report.Overflowed != 1 || !errors.Is(err, observable.ErrOverflow) {
		t.Fatalf("partial = %#v, %v", report, err)
	}
	if _, err := slow.Next(t.Context()); !errors.Is(err, observable.ErrOverflow) {
		t.Fatal(err)
	}
	next(t, fast, 2)
	if err := subject.Publish(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	next(t, fast, 3)
}

func TestCloseOnlyDetachesOneSubscriber(t *testing.T) {
	state := mustState(t, 1, observable.SubjectOptions[int]{})
	first, second := mustOpen(t, state), mustOpen(t, state)
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Next(t.Context()); !errors.Is(err, observable.ErrClosed) {
		t.Fatal(err)
	}
	next(t, second, 1)
	if err := state.Publish(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	next(t, second, 2)
}

func TestCloneIsolationAndNoPartialPublicationOnCloneFailure(t *testing.T) {
	failure := errors.New("clone failure")
	state := mustState(t, []int{1}, observable.SubjectOptions[[]int]{Clone: func(value []int) ([]int, error) {
		if len(value) > 0 && value[0] < 0 {
			return nil, failure
		}
		return slices.Clone(value), nil
	}})
	first, second := mustOpen(t, state), mustOpen(t, state)
	got, err := first.Next(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got[0] = 99
	next(t, second, []int{1})
	current, _, err := state.Current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current[0] = 88
	if err := state.Publish(t.Context(), []int{-1}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	current, present, err := state.Current(t.Context())
	if !reflect.DeepEqual(current, []int{1}) || !present || err != nil {
		t.Fatalf("current changed: %v, %v, %v", current, present, err)
	}
}

func TestBlockedNextCancellationAndConcurrentNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewPendingState[int](observable.SubjectOptions[int]{})
		if err != nil {
			t.Fatal(err)
		}
		s := mustOpen(t, state)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { _, err := s.Next(ctx); done <- err }()
		synctest.Wait()
		if _, err := s.Next(t.Context()); !errors.Is(err, observable.ErrConcurrentNext) {
			t.Fatal(err)
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		go func() { _, err := s.Next(t.Context()); done <- err }()
		synctest.Wait()
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, observable.ErrClosed) {
			t.Fatal(err)
		}
	})
}

func TestOpenCancellationDoesNotFailSharedSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewPendingState[int](observable.SubjectOptions[int]{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		s, err := state.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := s.Next(t.Context()); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := state.Publish(t.Context(), 7); err != nil {
			t.Fatal(err)
		}
		next(t, mustOpen(t, state), 7)
	})
}

func TestPublishCancellationWhileCloneInProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		subject, err := observable.NewSubject[int](observable.SubjectOptions[int]{Clone: func(value int) (int, error) {
			close(entered)
			<-release
			return value, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- subject.Publish(t.Context(), 1) }()
		<-entered
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := subject.Publish(ctx, 2); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestConcurrentPublishersShareSubscriberOrdering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		subject, err := observable.NewSubject[int](observable.SubjectOptions[int]{Buffer: 32})
		if err != nil {
			t.Fatal(err)
		}
		first, second := mustOpen(t, subject), mustOpen(t, subject)
		var workers sync.WaitGroup
		for value := range 32 {
			workers.Go(func() {
				if err := subject.Publish(t.Context(), value); err != nil {
					t.Error(err)
				}
			})
		}
		workers.Wait()
		seen := make(map[int]bool)
		for range 32 {
			a, err := first.Next(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			b, err := second.Next(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if a != b || seen[a] {
				t.Fatalf("inconsistent/duplicate ordering: %d, %d", a, b)
			}
			seen[a] = true
		}
	})
}

func TestAtomicReplayAndPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state := mustState(t, 0, observable.SubjectOptions[int]{})
		var stream observable.Stream[int]
		var workers sync.WaitGroup
		workers.Go(func() {
			var err error
			stream, err = state.Open(t.Context())
			if err != nil {
				t.Error(err)
			}
		})
		workers.Go(func() {
			if err := state.Publish(t.Context(), 1); err != nil {
				t.Error(err)
			}
		})
		workers.Wait()
		if err := state.Complete(); err != nil {
			t.Fatal(err)
		}
		var values []int
		for {
			value, err := stream.Next(t.Context())
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		if !reflect.DeepEqual(values, []int{1}) && !reflect.DeepEqual(values, []int{0, 1}) {
			t.Fatalf("non-atomic replay: %v", values)
		}
		if err := stream.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestProducerIsLazyAndCloseJoinsBlockedEmit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, stopped := false, false
		source := observable.FromProducer(func(ctx context.Context, emit func(int) error) error {
			started = true
			defer func() { stopped = true }()
			return emit(0)
		})
		if started {
			t.Fatal("producer activated during construction")
		}
		s := mustOpen(t, source)
		synctest.Wait()
		if !started {
			t.Fatal("producer not activated")
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !stopped {
			t.Fatal("Close did not join producer")
		}
	})
}

func TestProducerCloseDuringStartupCanContinueJoining(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		s := observable.FromProducer(func(context.Context, func(int) error) error { <-release; return nil })
		stream, err := s.Open(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := stream.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unjoined Close = %v", err)
		}
		close(release)
		if err := stream.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Next(t.Context()); !errors.Is(err, observable.ErrClosed) {
			t.Fatal(err)
		}
	})
}

func TestIteratorStopsEarlyAndJoinsCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cleaned := false
		source := observable.FromIterator(func(context.Context) iter.Seq2[int, error] {
			return func(yield func(int, error) bool) {
				defer func() { cleaned = true }()
				for value := range 10 {
					if !yield(value, nil) {
						return
					}
				}
			}
		})
		s := mustOpen(t, source)
		next(t, s, 0)
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !cleaned {
			t.Fatal("iterator cleanup not joined")
		}
	})
}

func TestProducerCompletionAndFailureRemainDistinct(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("producer failed")
		for _, terminal := range []error{nil, failure} {
			stream, err := observable.FromProducer(func(context.Context, func(int) error) error { return terminal }).Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := terminal
			if want == nil {
				want = io.EOF
			}
			if _, err := stream.Next(t.Context()); !errors.Is(err, want) {
				t.Fatalf("terminal = %v, want %v", err, want)
			}
			if err := stream.Close(t.Context()); !errors.Is(err, terminal) {
				t.Fatal(err)
			}
		}
	})
}

func TestChannelFactoryCleanupIsOwnedAndBorrowedChannelIsNotClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		values := make(chan int, 1)
		values <- 5
		cleanupCalls := 0
		source := observable.FromChannelFactory(func(context.Context) (<-chan int, func(context.Context) error, error) {
			return values, func(context.Context) error { cleanupCalls++; return nil }, nil
		})
		s := mustOpen(t, source)
		next(t, s, 5)
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if cleanupCalls != 1 {
			t.Fatalf("cleanup calls = %d", cleanupCalls)
		}
		values <- 6 // panics if the framework closed the borrowed channel
	})
}

func TestInvalidConstructionAndContexts(t *testing.T) {
	if _, err := observable.NewSubject[int](observable.SubjectOptions[int]{Buffer: -1}); !errors.Is(err, observable.ErrInvalidOptions) {
		t.Fatal(err)
	}
	state := mustState(t, 1, observable.SubjectOptions[int]{})
	if _, err := state.Open(nil); !errors.Is(err, observable.ErrInvalidOptions) {
		t.Fatal(err)
	}
	if err := state.Publish(nil, 2); !errors.Is(err, observable.ErrInvalidOptions) {
		t.Fatal(err)
	}
	if err := state.Fail(nil); !errors.Is(err, observable.ErrInvalidOptions) {
		t.Fatal(err)
	}
}

func ExampleState() {
	ctx := context.Background()
	state, err := observable.NewState([]string{}, observable.SubjectOptions[[]string]{Clone: func(value []string) ([]string, error) { return slices.Clone(value), nil }})
	if err != nil {
		panic(err)
	}
	stream, err := state.Open(ctx)
	if err != nil {
		panic(err)
	}
	initial, err := stream.Next(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(initial))
	if err := state.Publish(ctx, []string{"Ada"}); err != nil {
		panic(err)
	}
	current, err := stream.Next(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(current)
	if err := stream.Close(ctx); err != nil {
		panic(err)
	}
	// Output:
	// 0
	// [Ada]
}
