// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import (
	"context"
	"sync"
)

type entry struct {
	done  chan struct{}
	value any
	err   error
}
type ownedValue struct {
	key   Key
	value any
}

// owner coordinates admission, cached attempts, and exactly-once cooperative cleanup.
// All user callbacks execute outside its lock.
type owner struct {
	mu       sync.Mutex
	entries  map[Key]*entry
	values   []ownedValue
	active   int
	closing  bool
	drained  chan struct{}
	cleaning bool
	done     chan struct{}
	err      error
}

func newOwner() *owner { return &owner{entries: map[Key]*entry{}, done: make(chan struct{})} }
func (o *owner) admit() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closing {
		return failure("resolve", Key{}, nil, ErrClosed, nil)
	}
	if o.active == 0 {
		o.drained = make(chan struct{})
	}
	o.active++
	return nil
}
func (o *owner) release() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.active--
	if o.active == 0 {
		close(o.drained)
	}
}
func wait(ctx context.Context, done <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *owner) close(ctx context.Context, forceCleanup bool, cleanup func(context.Context) error) error {
	o.mu.Lock()
	o.closing = true
	if o.active != 0 {
		drained := o.drained
		o.mu.Unlock()
		if err := wait(ctx, drained); err != nil {
			return err
		}
		o.mu.Lock()
	}
	select {
	case <-o.done:
		err := o.err
		o.mu.Unlock()
		return err
	default:
	}
	if o.cleaning {
		done := o.done
		o.mu.Unlock()
		if forceCleanup {
			// Provider cleanup has begun: join existing scope cleanup before
			// releasing root dependencies, even when its context has expired.
			<-done
		} else if err := wait(ctx, done); err != nil {
			return err
		}
		o.mu.Lock()
		err := o.err
		o.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil && !forceCleanup {
		o.mu.Unlock()
		return err
	}
	o.cleaning = true
	o.mu.Unlock()
	err := cleanup(ctx)
	o.mu.Lock()
	o.err = err
	close(o.done)
	o.mu.Unlock()
	return err
}
