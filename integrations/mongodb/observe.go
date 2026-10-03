// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"context"
	"errors"

	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type findSource[T any] struct {
	watcher    *Watcher
	collection *Collection[T]
	filter     bson.Raw // private immutable instruction, never model rows
}

// Observe freezes a trusted selection using the collection's registry without
// I/O or goroutines. Pair it with the existing renderer for the same binding via
// RegisterObservable[M,A,Find[M]]. Open resolves tenant/query metadata and owns
// one subscriber; the watcher owns the shared database reader. Each Next returns
// a fresh detached Find instruction, initially only after cursor establishment,
// then on namespace invalidation. Rendering and authorization remain Arc-owned.
// This Source has no Current value: plain non-wait snapshots remain pending.
func Observe[T any](watcher *Watcher, collection *Collection[T], selection Find[T]) (observable.Source[Find[T]], error) {
	if watcher == nil || watcher.client == nil || watcher.lifetime == nil || collection == nil || collection.registry == nil || collection.client != watcher.client {
		return nil, ErrConfiguration
	}
	filter, err := freezeFilter(collection.registry, selection.Filter)
	if err != nil {
		return nil, err
	}
	if len(filter) > watcher.options.MaxFilterBytes {
		return nil, ErrLimit
	}
	return &findSource[T]{watcher, collection, filter}, nil
}

type findStream[T any] struct {
	source *findSource[T]
	sub    *watchSubscriber
	ctx    context.Context // independently owned subscription, not shared reader
}

func (source *findSource[T]) Open(ctx context.Context) (observable.Stream[Find[T]], error) {
	if ctx == nil {
		return nil, ErrConfiguration
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tenant, name := (tenancy.ID{}), ""
	if query, ok := queries.ContextFrom(ctx); ok {
		tenant, name = query.Tenant(), string(query.Name())
	}
	database, err := DatabaseName(source.collection.options.Database, tenant)
	if err != nil || !validCollection(database, source.collection.options.Name) {
		return nil, ErrConfiguration
	}
	key := queryKey{database, source.collection.options.Name, name}
	w := source.watcher
	w.mu.Lock()
	if w.closing || w.lifetime.Err() != nil {
		w.mu.Unlock()
		return nil, observable.ErrClosed
	}
	if len(w.subscribers) >= w.options.MaxSubscribers {
		w.mu.Unlock()
		return nil, ErrLimit
	}
	count := 0
	for s := range w.subscribers {
		if s.key == key {
			count++
		}
	}
	if count >= w.options.MaxSubscribersPerQuery {
		w.mu.Unlock()
		return nil, ErrLimit
	}
	d := w.databases[database]
	if d != nil && d.failure != nil {
		select {
		case <-d.done:
			if d.cleanupErr == nil {
				delete(w.databases, database)
				d = nil
			}
		default:
		}
		if d != nil {
			w.mu.Unlock()
			return nil, errors.Join(ErrResnapshotRequired, observable.ErrJoinPending)
		}
	}
	start := d == nil
	if start {
		if len(w.databases) >= w.options.MaxDatabases {
			w.mu.Unlock()
			return nil, ErrLimit
		}
		work, cancel := context.WithCancel(w.lifetime)
		d = &databaseWatch{name: database, ctx: work, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), subs: make(map[*watchSubscriber]struct{})}
		w.databases[database] = d
	}
	sub := &watchSubscriber{database: d, key: key, markers: make(chan struct{}, w.options.Buffer), terminal: make(chan struct{})}
	w.subscribers[sub] = struct{}{}
	d.subs[sub] = struct{}{}
	if d.live {
		w.enqueue(sub)
	}
	w.mu.Unlock()
	// Registration precedes both startup and initial handoff. A concurrent Close
	// can cancel this work before it starts, but still retains its done handle.
	if start {
		go w.readDatabase(d)
	}
	stream := &findStream[T]{source, sub, ctx}
	select {
	case <-d.ready:
	case <-ctx.Done():
		w.mu.Lock()
		w.stopSubscriber(sub, ctx.Err())
		w.mu.Unlock()
		return stream, ctx.Err()
	}
	w.mu.Lock()
	err = errors.Join(sub.failure, ctx.Err())
	if err != nil {
		w.stopSubscriber(sub, err)
	}
	w.mu.Unlock()
	return stream, err
}

func (s *findStream[T]) Next(ctx context.Context) (Find[T], error) {
	var zero Find[T]
	if ctx == nil {
		return zero, ErrConfiguration
	}
	w, sub := s.source.watcher, s.sub
	w.mu.Lock()
	if sub.closing || sub.removed {
		w.mu.Unlock()
		return zero, observable.ErrClosed
	}
	if sub.active != nil {
		w.mu.Unlock()
		return zero, observable.ErrConcurrentNext
	}
	sub.active = make(chan struct{})
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		close(sub.active)
		sub.active = nil
		w.mu.Unlock()
	}()
	if err := errors.Join(ctx.Err(), s.ctx.Err()); err != nil {
		return zero, err
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-s.ctx.Done():
		return zero, s.ctx.Err()
	case <-sub.terminal:
	case <-sub.markers:
	}
	w.mu.Lock()
	err := sub.failure
	w.mu.Unlock()
	if err != nil {
		return zero, err
	}
	filter, err := decodeFilter(s.source.filter)
	if err != nil {
		return zero, err
	}
	return Find[T]{Filter: filter}, nil
}

func (s *findStream[T]) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrConfiguration
	}
	w, sub := s.source.watcher, s.sub
	w.mu.Lock()
	sub.closing = true
	w.stopSubscriber(sub, observable.ErrClosed)
	active := sub.active
	w.mu.Unlock()
	if active != nil {
		if err := waitWatch(ctx, active); err != nil {
			return err
		}
	}
	w.mu.Lock()
	if !sub.removed {
		delete(w.subscribers, sub)
		delete(sub.database.subs, sub)
		sub.removed = true
	}
	w.mu.Unlock()
	// A subscriber owns no cursor/reader and must not stop the shared owner.
	return nil
}
