// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

// Any saved method, parent wrapper or callback will trip this guard after Open.
// The structural assertion below also excludes an unused retained context.
type guardedOpenContext struct {
	context.Context
	sealed atomic.Bool
}

func (c *guardedOpenContext) check() {
	if c.sealed.Load() {
		panic("stream accessed the original Open context after Open returned")
	}
}
func (c *guardedOpenContext) Deadline() (time.Time, bool) { c.check(); return c.Context.Deadline() }
func (c *guardedOpenContext) Done() <-chan struct{}       { c.check(); return c.Context.Done() }
func (c *guardedOpenContext) Err() error                  { c.check(); return c.Context.Err() }
func (c *guardedOpenContext) Value(key any) any           { c.check(); return c.Context.Value(key) }

type metadataGuardSource struct {
	source observable.Source[Find[author]]
	stream *findStream[author]
	probe  *guardedOpenContext
}

func (s *metadataGuardSource) Open(ctx context.Context) (observable.Stream[Find[author]], error) {
	if query, ok := queries.ContextFrom(ctx); !ok || query.Name() != "author.Private" {
		return nil, errors.New("fixture must contain actual Arc query metadata")
	}
	s.probe = &guardedOpenContext{Context: ctx}
	stream, err := s.source.Open(s.probe)
	if stream != nil {
		s.stream = stream.(*findStream[author])
	}
	s.probe.sealed.Store(true)
	return stream, err
}

func assertSubscriptionReleased(t *testing.T, stream *findStream[author]) {
	t.Helper()
	w := stream.source.watcher
	w.mu.Lock()
	defer w.mu.Unlock()
	if stream.subscriptionDone != nil || !stream.subscriptionDeadline.IsZero() || stream.subscriptionHasDeadline || stream.subscriptionErr != nil || stream.sub.active != nil || !stream.sub.removed {
		t.Fatal("joined stream retained subscription state")
	}
}

func TestFindStreamDoesNotRetainPerformerMetadataOrContextMethods(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, binding := testWatcher(t, WatcherOptions{}, newWatchTestCursor())
		guard := &metadataGuardSource{source: testObserve(t, w, binding)}
		pipeline := watchPipeline(t, guard, func(context.Context, *execution.Scope) (queries.Renderer[Find[author], []author], error) {
			return nil, errors.New("no rendering expected")
		}, queries.PipelineOptions{}, "Private")
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		observation, _, err := pipeline.Open(ctx, "author.Private", queries.Request{})
		if err != nil {
			t.Fatal(err)
		}
		// Exact allowed retained surface: neither context interfaces nor hidden
		// functions/wrappers can be added without this boundary check failing.
		allowed := map[string]reflect.Type{
			"source": reflect.TypeFor[*findSource[author]](), "sub": reflect.TypeFor[*watchSubscriber](),
			"subscriptionDone": reflect.TypeFor[<-chan struct{}](), "subscriptionDeadline": reflect.TypeFor[time.Time](),
			"subscriptionHasDeadline": reflect.TypeFor[bool](), "subscriptionErr": reflect.TypeFor[error](),
		}
		shape := reflect.TypeFor[findStream[author]]()
		if shape.NumField() != len(allowed) {
			t.Fatal("stream gained unaudited retained state")
		}
		for i := range shape.NumField() {
			field := shape.Field(i)
			if allowed[field.Name] != field.Type {
				t.Fatalf("unaudited retained field %s: %v", field.Name, field.Type)
			}
		}
		stream := guard.stream
		if stream.subscriptionDone != guard.probe.Context.Done() || stream.subscriptionDeadline.IsZero() {
			t.Fatal("subscription lifetime was not copied")
		}
		testWatchNext(t, stream)
		cancel()
		if _, err := stream.Next(context.Background()); !errors.Is(err, context.Canceled) {
			t.Fatalf("copied parent cancellation: %v", err)
		}
		testWatchClose(t, observation)
		assertSubscriptionReleased(t, stream)
		if err := pipeline.CloseObservations(context.Background()); err != nil {
			t.Fatal(err)
		}
		testWatchClose(t, w)
	})
}

func TestFindStreamSubscriptionCancellationClassification(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline bool
		late     bool
		manual   bool
		want     error
	}{
		{name: "parent cancellation", manual: true, want: context.Canceled},
		{name: "before deadline stays canceled", deadline: true, manual: true, want: context.Canceled},
		{name: "deadline while Next waits", deadline: true, want: context.DeadlineExceeded},
		{name: "cancel before deadline observed at deadline", deadline: true, late: true, manual: true, want: context.DeadlineExceeded},
		{name: "deadline first observed later", deadline: true, late: true, want: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, binding := testWatcher(t, WatcherOptions{}, newWatchTestCursor())
				parent, cancelParent := context.WithCancelCause(context.Background())
				defer cancelParent(nil)
				ctx := context.Context(parent)
				if tc.deadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(parent, 10*time.Second)
					defer stop()
				}
				stream, err := testObserve(t, w, binding).Open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				testWatchNext(t, stream)
				result := make(chan error, 1)
				if !tc.late {
					go func() { _, err := stream.Next(context.Background()); result <- err }()
					synctest.Wait()
				}
				if tc.manual {
					cancelParent(errors.New("private application cause"))
				}
				if tc.late || !tc.manual {
					time.Sleep(10 * time.Second) // fake time: exact copied deadline
				}
				if tc.late {
					_, err := stream.Next(context.Background())
					result <- err
				}
				if err := <-result; !errors.Is(err, tc.want) {
					t.Fatalf("subscription cancellation = %v, want %v", err, tc.want)
				}
				// Classification is fixed at first observation, not recalculated
				// when a previously observed early cancellation crosses deadline.
				time.Sleep(11 * time.Second)
				if _, err := stream.Next(context.Background()); !errors.Is(err, tc.want) {
					t.Fatalf("classification changed on later Next: %v", err)
				}
				testWatchClose(t, stream)
				assertSubscriptionReleased(t, stream.(*findStream[author]))
				testWatchClose(t, w)
			})
		})
	}
}

func TestFindStreamNextPropagatesItsActualContextError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, binding := testWatcher(t, WatcherOptions{}, newWatchTestCursor())
		stream := testWatchOpen(t, testObserve(t, w, binding))
		testWatchNext(t, stream)
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		result := make(chan error, 1)
		go func() { _, err := stream.Next(ctx); result <- err }()
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Next deadline error: %v", err)
		}
		// A custom Context.Err identity is read from the supplied Next
		// context, not reconstructed from the subscription's deadline.
		actual := errors.New("custom Next error")
		custom := &nextErrorContext{done: make(chan struct{}), err: actual}
		close(custom.done)
		if _, err := stream.Next(custom); !errors.Is(err, actual) {
			t.Fatalf("actual Next error identity lost: %v", err)
		}
		testWatchClose(t, stream)
		testWatchClose(t, w)
	})
}

type nextErrorContext struct {
	done chan struct{}
	err  error
}

func (*nextErrorContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *nextErrorContext) Done() <-chan struct{}     { return c.done }
func (c *nextErrorContext) Err() error                { return c.err }
func (*nextErrorContext) Value(any) any               { return nil }

func TestFindStreamContinuedConcurrentCloseReleasesOnlyAfterNextJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{}, cursor)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		handle, err := testObserve(t, w, binding).Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stream := handle.(*findStream[author])
		testWatchNext(t, stream)
		// Hold a real Next inside its caller's Err method. This deterministic
		// barrier makes incomplete Close and subsequent concurrent joins visible.
		entered, release := make(chan struct{}), make(chan struct{})
		nextCtx := &blockedNextContext{Context: context.Background(), entered: entered, release: release}
		next := make(chan error, 1)
		go func() { _, err := stream.Next(nextCtx); next <- err }()
		<-entered
		budget, stop := context.WithCancel(context.Background())
		stop()
		if err := stream.Close(budget); !errors.Is(err, observable.ErrJoinPending) {
			t.Fatalf("blocked Next join: %v", err)
		}
		if stream.subscriptionDone == nil || stream.subscriptionDeadline.IsZero() || stream.sub.removed {
			t.Fatal("pending join released state")
		}
		closed := make(chan error, 2)
		for range 2 {
			go func() { closed <- stream.Close(context.Background()) }()
		}
		synctest.Wait()
		if len(closed) != 0 {
			t.Fatal("Close completed before active Next joined")
		}
		close(release)
		if err := <-next; !errors.Is(err, observable.ErrClosed) {
			t.Fatalf("Close did not terminate active Next: %v", err)
		}
		for range 2 {
			if err := <-closed; err != nil {
				t.Fatal(err)
			}
		}
		assertSubscriptionReleased(t, stream)
		if _, err := stream.Next(context.Background()); !errors.Is(err, observable.ErrClosed) {
			t.Fatalf("Next after Close: %v", err)
		}
		testWatchClose(t, stream)
		if cursor.closes.Load() != 0 {
			t.Fatal("subscriber close disposed the shared cursor")
		}
		testWatchClose(t, w)
		if cursor.closes.Load() != 1 {
			t.Fatal("owner cleanup was not exactly once")
		}
	})
}

type blockedNextContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
}

func (c *blockedNextContext) Err() error {
	close(c.entered)
	<-c.release
	return nil
}
