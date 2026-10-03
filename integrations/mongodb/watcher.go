// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cratis/arc.go/observable"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// ErrResnapshotRequired terminates a generation whose continuity is unknown.
// Close the old observation and explicitly Open a new one for a full baseline.
// There is no automatic resume or delivery-baseline reset inside an observation.
var ErrResnapshotRequired = errors.New("MongoDB observation requires a new snapshot")

// WatcherOptions bounds database readers and retained subscriber state. Zero
// selects each documented default; negative values fail construction.
type WatcherOptions struct {
	// MaxDatabases defaults to 32; idle live and unjoined readers count.
	MaxDatabases int
	// MaxSubscribers defaults to 1024, including opening and retired streams.
	MaxSubscribers int
	// MaxSubscribersPerQuery defaults to 64 per database/collection/logical name.
	MaxSubscribersPerQuery int
	// Buffer defaults to 16 invalidation markers, never model documents.
	Buffer int
	// MaxFilterBytes defaults to 64 KiB of frozen BSON per source instruction.
	MaxFilterBytes int
	// OpenTimeout defaults to ten seconds for cursor establishment.
	OpenTimeout time.Duration
	// CleanupTimeout defaults to five seconds for the single cursor Close call.
	CleanupTimeout time.Duration
}

// Watcher lazily shares one database cursor per resolved database on its borrowed
// client. Construct with NewWatcher. Safe for concurrent use; there is no global
// cache. The explicit application lifetime, not any request, owns its readers.
// Close cancels all readers before joining; only the application disconnects the
// client, after Arc observations and this watcher have drained.
type Watcher struct {
	mu          sync.Mutex
	lifetime    context.Context
	client      *mongo.Client
	options     WatcherOptions
	closing     bool
	databases   map[string]*databaseWatch
	subscribers map[*watchSubscriber]struct{}
	open        func(context.Context, string) (snapshotCursor, error)
}

type databaseWatch struct {
	name       string
	ctx        context.Context
	cancel     context.CancelFunc
	ready      chan struct{}
	done       chan struct{}
	live       bool
	failure    error
	cleanupErr error
	cursor     snapshotCursor // retained on failed disposal; only reader operates it
	subs       map[*watchSubscriber]struct{}
}

type queryKey struct{ database, collection, name string }

type watchSubscriber struct {
	database *databaseWatch
	key      queryKey
	markers  chan struct{}
	terminal chan struct{}
	failure  error
	closing  bool
	removed  bool
	active   chan struct{}
}

// NewWatcher validates options without I/O or goroutines. lifetime must be an
// application-owned context. client is borrowed and is never disconnected here.
func NewWatcher(lifetime context.Context, client *mongo.Client, config WatcherOptions) (*Watcher, error) {
	if lifetime == nil || client == nil || config.MaxDatabases < 0 || config.MaxSubscribers < 0 || config.MaxSubscribersPerQuery < 0 || config.Buffer < 0 || config.MaxFilterBytes < 0 || config.OpenTimeout < 0 || config.CleanupTimeout < 0 {
		return nil, ErrConfiguration
	}
	defaults := []struct {
		value    *int
		fallback int
	}{
		{&config.MaxDatabases, 32}, {&config.MaxSubscribers, 1024},
		{&config.MaxSubscribersPerQuery, 64}, {&config.Buffer, 16}, {&config.MaxFilterBytes, 64 << 10},
	}
	for _, d := range defaults {
		if *d.value == 0 {
			*d.value = d.fallback
		}
	}
	if config.OpenTimeout == 0 {
		config.OpenTimeout = 10 * time.Second
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = 5 * time.Second
	}
	w := &Watcher{lifetime: lifetime, client: client, options: config, databases: make(map[string]*databaseWatch), subscribers: make(map[*watchSubscriber]struct{})}
	w.open = func(ctx context.Context, database string) (snapshotCursor, error) {
		// Database.Watch hides automatic resume inside Next. An ordinary public
		// aggregate cursor gives this owner strict terminal-on-loss semantics.
		pipeline := mongo.Pipeline{
			{{Key: "$changeStream", Value: bson.D{}}},
			{{Key: "$project", Value: bson.D{{Key: "operationType", Value: 1}, {Key: "ns", Value: 1}, {Key: "to", Value: 1}}}},
		}
		cursor, err := client.Database(database, options.Database().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority())).Aggregate(ctx, pipeline, options.Aggregate().SetBatchSize(64))
		if cursor == nil {
			return nil, err
		}
		return driverCursor{cursor}, err
	}
	return w, nil
}

// stopSubscriber requires w.mu. Terminal states discard all queued markers.
func (w *Watcher) stopSubscriber(s *watchSubscriber, err error) {
	if s.failure != nil {
		return
	}
	s.failure = err
	for {
		select {
		case <-s.markers:
		default:
			close(s.terminal)
			return
		}
	}
}

func (w *Watcher) enqueue(s *watchSubscriber) {
	if s.failure != nil {
		return
	}
	select {
	case s.markers <- struct{}{}:
	default:
		w.stopSubscriber(s, observable.ErrOverflow)
	}
}

func (w *Watcher) failDatabase(d *databaseWatch, err error) {
	// All registration, initial handoff and fanout share this ordering lock.
	w.mu.Lock()
	if d.failure == nil {
		d.failure = err
	}
	d.live = false
	for s := range d.subs {
		w.stopSubscriber(s, d.failure)
	}
	w.mu.Unlock()
	d.cancel()
}

func (w *Watcher) readDatabase(d *databaseWatch) {
	var cursor snapshotCursor
	ready := false
	defer func() {
		if recover() != nil {
			w.failDatabase(d, errors.Join(ErrResnapshotRequired, observable.ErrProducerPanic))
		}
		if !ready {
			close(d.ready)
		}
		// Only this reader touches/disposes the cursor. Cancellation is not a
		// join, and even an over-budget driver must finish before done closes.
		var cleanupErr error
		if cursor != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(d.ctx), w.options.CleanupTimeout)
			cleanupErr = closeWatchCursor(cleanup, cursor)
			cancel()
		}
		d.cancel()
		w.mu.Lock()
		d.cleanupErr = cleanupErr
		if cleanupErr == nil {
			d.cursor = nil
		}
		close(d.done)
		w.mu.Unlock()
	}()
	opening, cancel := context.WithTimeout(d.ctx, w.options.OpenTimeout)
	var err error
	cursor, err = w.open(opening, d.name)
	w.mu.Lock()
	d.cursor = cursor
	w.mu.Unlock()
	err = errors.Join(err, opening.Err())
	cancel()
	if err != nil || cursor == nil {
		w.failDatabase(d, errors.Join(ErrResnapshotRequired, err))
		return
	}
	w.mu.Lock()
	if d.ctx.Err() == nil && d.failure == nil {
		d.live = true
		for s := range d.subs {
			w.enqueue(s)
		}
	}
	close(d.ready)
	ready = true
	w.mu.Unlock()
	if d.ctx.Err() != nil {
		w.failDatabase(d, observable.ErrClosed)
		return
	}
	for cursor.next(d.ctx) {
		if d.ctx.Err() != nil {
			break
		}
		event, err := watchEvent(cursor.current())
		if err != nil {
			w.failDatabase(d, ErrResnapshotRequired)
			return
		}
		if event.terminal {
			// Coordinate changes retire the whole shared database generation.
			w.failDatabase(d, ErrResnapshotRequired)
			return
		}
		w.mu.Lock()
		for s := range d.subs {
			if event.database == d.name && event.collection == s.key.collection {
				w.enqueue(s)
			}
		}
		w.mu.Unlock()
	}
	if d.ctx.Err() != nil {
		w.failDatabase(d, observable.ErrClosed)
	} else {
		w.failDatabase(d, errors.Join(ErrResnapshotRequired, &operationError{"watch cursor", cursor.err()}))
	}
}

func closeWatchCursor(ctx context.Context, cursor snapshotCursor) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.Join(observable.ErrJoinPending, observable.ErrProducerPanic)
		}
	}()
	// Do not treat an expired cleanup budget as success, even if the driver
	// returns nil. Failed disposal retains the database entry for inspection.
	return errors.Join(cursor.close(ctx), ctx.Err())
}

type namespaceEvent struct {
	database, collection string
	terminal             bool
}

func watchEvent(raw bson.Raw) (namespaceEvent, error) {
	if raw.Validate() != nil {
		return namespaceEvent{}, ErrValue
	}
	op, ok := raw.Lookup("operationType").StringValueOK()
	if !ok {
		return namespaceEvent{}, ErrValue
	}
	switch op {
	case "drop", "rename", "dropDatabase", "invalidate":
		return namespaceEvent{terminal: true}, nil
	case "insert", "update", "replace", "delete":
		db, dbOK := raw.Lookup("ns", "db").StringValueOK()
		collection, collOK := raw.Lookup("ns", "coll").StringValueOK()
		if !dbOK || !collOK || db == "" || collection == "" {
			return namespaceEvent{}, ErrValue
		}
		return namespaceEvent{database: db, collection: collection}, nil
	default:
		// Unknown operation semantics cannot certify continuity.
		return namespaceEvent{}, ErrValue
	}
}

func waitWatch(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Join(observable.ErrJoinPending, ctx.Err())
	}
}

// Close stops admission, cancels every reader first, then joins readers, cursor
// cleanup and active Next calls. It never disconnects the client. Repeat after a
// wait timeout with a fresh context; ownership is retained and disposal is not
// repeated. A cursor disposal failure remains inspectable on subsequent calls.
func (w *Watcher) Close(ctx context.Context) error {
	if w == nil || ctx == nil || w.lifetime == nil {
		return ErrConfiguration
	}
	w.mu.Lock()
	w.closing = true
	databases := make([]*databaseWatch, 0, len(w.databases))
	for _, d := range w.databases {
		databases = append(databases, d)
	}
	active := make([]<-chan struct{}, 0, len(w.subscribers))
	for s := range w.subscribers {
		w.stopSubscriber(s, observable.ErrClosed)
		s.closing = true
		if s.active != nil {
			active = append(active, s.active)
		}
	}
	w.mu.Unlock()
	for _, d := range databases {
		d.cancel()
	}
	var result error
	for _, d := range databases {
		if err := waitWatch(ctx, d.done); err != nil {
			return errors.Join(result, err)
		}
		w.mu.Lock()
		result = errors.Join(result, d.cleanupErr)
		w.mu.Unlock()
	}
	for _, done := range active {
		if err := waitWatch(ctx, done); err != nil {
			return errors.Join(result, err)
		}
	}
	w.mu.Lock()
	for s := range w.subscribers {
		delete(s.database.subs, s)
		s.removed = true
		delete(w.subscribers, s)
	}
	w.mu.Unlock()
	return result
}
