// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/observable"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type watchTestCursor struct {
	events     chan bson.Raw
	currentRaw bson.Raw
	readErr    error
	closes     atomic.Int32
	reading    atomic.Bool
	closeCall  func(context.Context) error
}

func newWatchTestCursor() *watchTestCursor {
	return &watchTestCursor{events: make(chan bson.Raw)}
}
func (c *watchTestCursor) next(ctx context.Context) bool {
	c.reading.Store(true)
	defer c.reading.Store(false)
	select {
	case <-ctx.Done():
		return false
	case raw, ok := <-c.events:
		c.currentRaw = raw
		return ok
	}
}
func (c *watchTestCursor) current() bson.Raw { return c.currentRaw }
func (c *watchTestCursor) err() error        { return c.readErr }
func (c *watchTestCursor) close(ctx context.Context) error {
	c.closes.Add(1)
	if c.reading.Load() {
		return errors.New("disposed cursor beneath reader")
	}
	if c.closeCall != nil {
		return c.closeCall(ctx)
	}
	return nil
}

func testWatchBinding[T any](t *testing.T, client *mongo.Client, database, collection string) *Collection[T] {
	t.Helper()
	binding, err := NewCollection[T](client, CollectionOptions{Database: database, Name: collection, Ownership: ApplicationOwned})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}
func testWatcher(t *testing.T, config WatcherOptions, cursor *watchTestCursor) (*Watcher, *Collection[author]) {
	t.Helper()
	client := &mongo.Client{}
	w, err := NewWatcher(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	w.open = func(context.Context, string) (snapshotCursor, error) { return cursor, nil }
	return w, testWatchBinding[author](t, client, "Library", "Authors")
}
func testObserve[T any](t *testing.T, w *Watcher, binding *Collection[T]) observable.Source[Find[T]] {
	t.Helper()
	source, err := Observe(w, binding, Find[T]{Filter: bson.D{{Key: "enabled", Value: true}}})
	if err != nil {
		t.Fatal(err)
	}
	return source
}
func testWatchOpen[T any](t *testing.T, source observable.Source[Find[T]]) observable.Stream[Find[T]] {
	t.Helper()
	stream, err := source.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return stream
}
func testWatchNext[T any](t *testing.T, stream observable.Stream[Find[T]]) Find[T] {
	t.Helper()
	value, err := stream.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func testWatchClose(t *testing.T, owner interface{ Close(context.Context) error }) {
	t.Helper()
	if err := owner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func changeEvent(t *testing.T, operation, database, collection string) bson.Raw {
	t.Helper()
	return rawDocument(t, bson.D{{Key: "operationType", Value: operation}, {Key: "ns", Value: bson.D{{Key: "db", Value: database}, {Key: "coll", Value: collection}}}})
}

func TestWatcherConstructionAndObserveAreInert(t *testing.T) {
	cursor := newWatchTestCursor()
	w, binding := testWatcher(t, WatcherOptions{}, cursor)
	w.open = func(context.Context, string) (snapshotCursor, error) {
		t.Error("constructor opened driver")
		return nil, ErrValue
	}
	selection := Find[author]{Filter: bson.D{{Key: "nested", Value: bson.D{{Key: "n", Value: int64(1)}}}}}
	source, err := Observe(w, binding, selection)
	if err != nil {
		t.Fatal(err)
	}
	selection.Filter[0].Value.(bson.D)[0].Value = int64(2)
	frozen, err := decodeFilter(source.(*findSource[author]).filter)
	if err != nil || frozen[0].Value.(bson.D)[0].Value != int64(1) {
		t.Fatal("Observe retained mutable graph")
	}
	if _, ok := source.(observable.CurrentSource[Find[author]]); ok {
		t.Fatal("Source must not advertise synthetic Current")
	}
	if len(w.databases) != 0 || cursor.closes.Load() != 0 {
		t.Fatal("construction activated work")
	}
	for _, config := range []WatcherOptions{{MaxDatabases: -1}, {MaxSubscribers: -1}, {MaxSubscribersPerQuery: -1}, {Buffer: -1}, {MaxFilterBytes: -1}, {OpenTimeout: -1}, {CleanupTimeout: -1}} {
		if _, err := NewWatcher(context.Background(), binding.client, config); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("negative option: %v", err)
		}
	}
	//nolint:staticcheck // Deliberately exercise the invalid nil-lifetime boundary.
	if _, err := NewWatcher(nil, binding.client, WatcherOptions{}); !errors.Is(err, ErrConfiguration) {
		t.Fatal("nil lifetime")
	}
	if _, err := NewWatcher(context.Background(), nil, WatcherOptions{}); !errors.Is(err, ErrConfiguration) {
		t.Fatal("nil client")
	}
	other := testWatchBinding[author](t, &mongo.Client{}, "Library", "Authors")
	if _, err := Observe(w, other, Find[author]{}); !errors.Is(err, ErrConfiguration) {
		t.Fatal("client pointer identity ignored")
	}
	w.options.MaxFilterBytes = 5
	if _, err := Observe(w, binding, selection); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded filter")
	}
	testWatchClose(t, w)
}

func TestWatchOpenEstablishesCursorBeforeInitialHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{}, cursor)
		opening := make(chan struct{})
		release := make(chan struct{})
		w.open = func(context.Context, string) (snapshotCursor, error) {
			close(opening)
			<-release
			return cursor, nil
		}
		source := testObserve(t, w, binding)
		opened := make(chan observable.Stream[Find[author]], 1)
		go func() { opened <- testWatchOpen(t, source) }()
		<-opening
		synctest.Wait()
		if len(opened) != 0 {
			t.Fatal("snapshot enabled before cursor establishment")
		}
		w.mu.Lock()
		if len(w.subscribers) != 1 {
			t.Fatal("opening subscriber not reserved")
		}
		for sub := range w.subscribers {
			if len(sub.markers) != 0 {
				t.Fatal("premature initial marker")
			}
		}
		w.mu.Unlock()
		close(release)
		stream := <-opened
		cursor.events <- changeEvent(t, "insert", "Library", "Authors")
		synctest.Wait()
		first := testWatchNext(t, stream)
		first.Filter[0].Value = false
		second := testWatchNext(t, stream)
		if second.Filter[0].Value != true {
			t.Fatal("instructions share mutable storage")
		}
		testWatchClose(t, stream)
		testWatchClose(t, w)
		if cursor.closes.Load() != 1 {
			t.Fatal("cursor cleanup not exactly once")
		}
	})
}

func TestWatcherConcurrentAttachAndFanoutRemainOrdered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{Buffer: 64}, cursor)
		var opens atomic.Int32
		w.open = func(context.Context, string) (snapshotCursor, error) { opens.Add(1); return cursor, nil }
		source := testObserve(t, w, binding)
		first := testWatchOpen(t, source)
		streams := make(chan observable.Stream[Find[author]], 16)
		for range 16 {
			go func() { streams <- testWatchOpen(t, testObserve(t, w, binding)) }()
		}
		go func() { cursor.events <- changeEvent(t, "update", "Library", "Authors") }()
		synctest.Wait()
		for range 16 {
			stream := <-streams
			// Every attached subscriber has its own baseline, whether it joined
			// before or after the racing update. Neither ordering loses baseline.
			testWatchNext(t, stream)
			testWatchClose(t, stream)
		}
		testWatchNext(t, first)
		testWatchNext(t, first)
		if opens.Load() != 1 {
			t.Fatalf("shared database opened %d cursors", opens.Load())
		}
		testWatchClose(t, first)
		testWatchClose(t, w)
	})
}

type watchedBook struct {
	ID int32 `json:"id" bson:"_id"`
}

func TestWatcherDifferentModelsShareDatabaseNotNamespace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, authors := testWatcher(t, WatcherOptions{}, cursor)
		books := testWatchBinding[watchedBook](t, w.client, "Library", "Books")
		a := testWatchOpen(t, testObserve(t, w, authors))
		b := testWatchOpen(t, testObserve(t, w, books))
		testWatchNext(t, a)
		testWatchNext(t, b)
		cursor.events <- changeEvent(t, "delete", "Library", "Books")
		synctest.Wait()
		w.mu.Lock()
		if len(w.databases) != 1 || len(a.(*findStream[author]).sub.markers) != 0 || len(b.(*findStream[watchedBook]).sub.markers) != 1 {
			t.Fatal("model keyed watch or cross-namespace invalidation")
		}
		w.mu.Unlock()
		testWatchNext(t, b)
		testWatchClose(t, a)
		cursor.events <- changeEvent(t, "replace", "Library", "Books")
		testWatchNext(t, b)
		testWatchClose(t, b)
		testWatchClose(t, w)
	})
}

func TestWatchOverflowIsSubscriberLocalAndRetiredCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{Buffer: 1, MaxSubscribersPerQuery: 2}, cursor)
		source := testObserve(t, w, binding)
		slow := testWatchOpen(t, source)
		fast := testWatchOpen(t, testObserve(t, w, binding))
		testWatchNext(t, fast)
		cursor.events <- changeEvent(t, "update", "Library", "Authors")
		synctest.Wait()
		if _, err := slow.Next(context.Background()); !errors.Is(err, observable.ErrOverflow) {
			t.Fatalf("slow overflow: %v", err)
		}
		testWatchNext(t, fast)
		if _, err := source.Open(context.Background()); !errors.Is(err, ErrLimit) {
			t.Fatalf("retired stream bypassed query admission: %v", err)
		}
		testWatchClose(t, slow)
		third := testWatchOpen(t, source)
		testWatchNext(t, third)
		testWatchClose(t, third)
		testWatchClose(t, fast)
		testWatchClose(t, w)
	})
}

func TestWatchLimitsIncludeOpeningAndIdleDatabases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{MaxDatabases: 1, MaxSubscribers: 1}, cursor)
		opened := make(chan struct{})
		release := make(chan struct{})
		w.open = func(context.Context, string) (snapshotCursor, error) { close(opened); <-release; return cursor, nil }
		source := testObserve(t, w, binding)
		streamResult := make(chan observable.Stream[Find[author]], 1)
		go func() { streamResult <- testWatchOpen(t, source) }()
		<-opened
		if _, err := source.Open(context.Background()); !errors.Is(err, ErrLimit) {
			t.Fatal("opening subscriber not counted")
		}
		close(release)
		stream := <-streamResult
		testWatchClose(t, stream)
		otherDB := testWatchBinding[author](t, w.client, "Other", "Authors")
		if _, err := testObserve(t, w, otherDB).Open(context.Background()); !errors.Is(err, ErrLimit) {
			t.Fatalf("idle live database not counted: %v", err)
		}
		testWatchClose(t, w)
	})
}

func TestWatchCursorLossAndCoordinateChangesRequireExplicitReopen(t *testing.T) {
	for _, operation := range []string{"cursor loss", "drop", "rename", "dropDatabase", "invalidate", "unknown", "malformed"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cursor := newWatchTestCursor()
				w, binding := testWatcher(t, WatcherOptions{}, cursor)
				source := testObserve(t, w, binding)
				old := testWatchOpen(t, source)
				testWatchNext(t, old)
				switch operation {
				case "cursor loss":
					cursor.readErr = errBoundary
					close(cursor.events)
				case "malformed":
					cursor.events <- bson.Raw{1, 2, 3}
				default:
					cursor.events <- changeEvent(t, operation, "Library", "Authors")
				}
				synctest.Wait()
				if _, err := old.Next(context.Background()); !errors.Is(err, ErrResnapshotRequired) {
					t.Fatalf("loss certified success: %v", err)
				}
				if cursor.closes.Load() != 1 {
					t.Fatal("lost cursor not joined")
				}
				fresh := newWatchTestCursor()
				w.open = func(context.Context, string) (snapshotCursor, error) { return fresh, nil }
				next := testWatchOpen(t, source)
				testWatchNext(t, next) // explicit reopen always starts a baseline
				testWatchClose(t, old)
				testWatchClose(t, next)
				testWatchClose(t, w)
			})
		})
	}
}

func TestCanceledStartupRetainsWatcherOwnerAndSharedLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{}, cursor)
		entered := make(chan struct{})
		release := make(chan struct{})
		w.open = func(context.Context, string) (snapshotCursor, error) { close(entered); <-release; return cursor, nil }
		ctx, cancel := context.WithCancel(context.Background())
		type openedStream struct {
			stream observable.Stream[Find[author]]
			err    error
		}
		result := make(chan openedStream, 1)
		go func() { stream, err := testObserve(t, w, binding).Open(ctx); result <- openedStream{stream, err} }()
		<-entered
		cancel()
		opened := <-result
		if opened.stream == nil || !errors.Is(opened.err, context.Canceled) {
			t.Fatal("failed startup lost cleanup handle")
		}
		testWatchClose(t, opened.stream) // only subscriber-owned work
		budget, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := w.Close(budget); !errors.Is(err, observable.ErrJoinPending) {
			t.Fatalf("unjoined opening owner discarded: %v", err)
		}
		if cursor.closes.Load() != 0 {
			t.Fatal("cursor disposed under opening work")
		}
		close(release)
		testWatchClose(t, w)
		if cursor.closes.Load() != 1 {
			t.Fatal("startup continuation cleanup")
		}
	})
}

func TestStreamCloseWakesAndJoinsOnlyItsNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{}, cursor)
		source := testObserve(t, w, binding)
		first := testWatchOpen(t, source)
		second := testWatchOpen(t, source)
		testWatchNext(t, first)
		testWatchNext(t, second)
		result := make(chan error, 1)
		go func() { _, err := first.Next(context.Background()); result <- err }()
		synctest.Wait()
		if _, err := first.Next(context.Background()); !errors.Is(err, observable.ErrConcurrentNext) {
			t.Fatal("concurrent Next accepted")
		}
		testWatchClose(t, first)
		if !errors.Is(<-result, observable.ErrClosed) || cursor.closes.Load() != 0 {
			t.Fatal("subscriber close failed to wake Next or closed shared reader")
		}
		cursor.events <- changeEvent(t, "insert", "Library", "Authors")
		testWatchNext(t, second)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _, err := second.Next(ctx); result <- err }()
		synctest.Wait()
		cancel()
		if !errors.Is(<-result, context.Canceled) {
			t.Fatal("Next cancellation ignored")
		}
		testWatchClose(t, second)
		testWatchClose(t, first)
		testWatchClose(t, w)
	})
}

func TestWatcherCloseCancelsAllReadersBeforeCleanupJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := newWatchTestCursor(), newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{}, first)
		blockCleanup := make(chan struct{})
		entered := make(chan struct{})
		first.closeCall = func(context.Context) error { close(entered); <-blockCleanup; return nil }
		w.open = func(_ context.Context, database string) (snapshotCursor, error) {
			if database == "Library" {
				return first, nil
			}
			return second, nil
		}
		a := testWatchOpen(t, testObserve(t, w, binding))
		b := testWatchOpen(t, testObserve(t, w, testWatchBinding[author](t, w.client, "Other", "Authors")))
		result := make(chan error, 1)
		go func() {
			budget, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result <- w.Close(budget)
		}()
		<-entered
		synctest.Wait()
		if second.closes.Load() != 1 || !errors.Is(<-result, observable.ErrJoinPending) {
			t.Fatal("shutdown waited on one cleanup before canceling others")
		}
		close(blockCleanup)
		testWatchClose(t, w)
		testWatchClose(t, w)
		testWatchClose(t, a)
		testWatchClose(t, b)
		if first.closes.Load() != 1 || second.closes.Load() != 1 {
			t.Fatal("cleanup side effects repeated")
		}
	})
}

func TestFailedGenerationNotReplacedBeforeCursorCleanupJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{}, cursor)
		entered, release := make(chan struct{}), make(chan struct{})
		cursor.closeCall = func(context.Context) error { close(entered); <-release; return nil }
		source := testObserve(t, w, binding)
		stream := testWatchOpen(t, source)
		cursor.events <- changeEvent(t, "drop", "Library", "Authors")
		<-entered
		if _, err := source.Open(context.Background()); !errors.Is(err, observable.ErrJoinPending) {
			t.Fatal("replacement disposed beneath reader")
		}
		testWatchClose(t, stream)
		close(release)
		synctest.Wait()
		fresh := newWatchTestCursor()
		w.open = func(context.Context, string) (snapshotCursor, error) { return fresh, nil }
		stream = testWatchOpen(t, source)
		testWatchNext(t, stream)
		testWatchClose(t, stream)
		testWatchClose(t, w)
	})
}

func TestOpeningTimeoutAndCleanupBudgetFailureRetainOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{OpenTimeout: time.Second, CleanupTimeout: time.Second}, cursor)
		w.open = func(ctx context.Context, _ string) (snapshotCursor, error) { <-ctx.Done(); return cursor, ctx.Err() }
		cursor.closeCall = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
		stream, err := testObserve(t, w, binding).Open(context.Background())
		if stream == nil || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrResnapshotRequired) {
			t.Fatalf("opening timeout: %v", err)
		}
		testWatchClose(t, stream)
		if err := w.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cleanup timeout certified success: %v", err)
		}
		if err := w.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) || cursor.closes.Load() != 1 {
			t.Fatal("failed cleanup ownership/effects lost")
		}
	})
}
