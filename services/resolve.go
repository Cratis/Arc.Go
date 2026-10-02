// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import (
	"context"
	"errors"
	"slices"
	"time"
)

// Resolve returns the exact registered T. It checks cancellation and scope security,
// shares singleton/scoped attempts, and constructs each transient independently.
// Ordinary failed attempts reach current waiters but are not cached. A live waiter
// retries an attempt canceled by its creator. Waiter cancellation never cancels
// the creator. Returned services are borrowed
// until their owning scope/provider closes; disposal does not commit effects.
func Resolve[T any](ctx context.Context, scope *Scope) (T, error) {
	var zero T
	key := KeyFor[T]()
	if scope == nil || scope.state == nil || ctx == nil {
		return zero, failure("resolve", key, nil, ErrInvalidScope, nil)
	}
	if scope.view != nil {
		if err := scope.view.enter(key); err != nil {
			return zero, err
		}
		defer scope.view.leave()
	} else {
		if err := scope.state.provider.root.admit(); err != nil {
			return zero, err
		}
		defer scope.state.provider.root.release()
		if err := scope.state.owner.admit(); err != nil {
			return zero, err
		}
		defer scope.state.owner.release()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := scope.CheckContext(ctx); err != nil {
		return zero, err
	}
	var path []Key
	root := false
	if scope.view != nil {
		path = scope.view.path
		root = scope.view.root
	}
	value, err := scope.resolve(ctx, key, path, root)
	if err != nil {
		return zero, err
	}
	return value.(T), nil
}
func appendPath(path []Key, key Key) []Key { return append(slices.Clone(path), key) }

// valuesHidden preserves cancellation/deadlines, without exposing any request values.
type valuesHidden struct{ context.Context }

func (valuesHidden) Value(any) any { return nil }

func (s *Scope) resolve(ctx context.Context, key Key, path []Key, root bool) (any, error) {
	b, ok := s.state.provider.bindings[key]
	if !ok {
		return nil, failure("resolve", key, appendPath(path, key), ErrMissing, nil)
	}
	root = root || b.lifetime == Singleton
	if root {
		ctx = valuesHidden{ctx}
	}
	if b.borrowed {
		return b.value, nil
	}
	owner := s.state.owner
	if root {
		owner = s.state.provider.root
	}
	path = appendPath(path, key)
	if b.lifetime == Transient {
		return s.construct(ctx, key, b, path, root, owner)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		owner.mu.Lock()
		if existing, ok := owner.entries[key]; ok {
			owner.mu.Unlock()
			if err := wait(ctx, existing.done); err != nil {
				return nil, err
			}
			if errors.Is(existing.err, context.Canceled) || errors.Is(existing.err, context.DeadlineExceeded) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				// The creator's lifetime is not the waiter's lifetime. Failed
				// entries have been removed; retry or join the next attempt.
				continue
			}
			return existing.value, existing.err
		}
		attempt := &entry{done: make(chan struct{})}
		owner.entries[key] = attempt
		owner.mu.Unlock()
		value, err := s.construct(ctx, key, b, path, root, owner)
		owner.mu.Lock()
		attempt.value, attempt.err = value, err
		if err != nil {
			delete(owner.entries, key)
		}
		close(attempt.done)
		owner.mu.Unlock()
		return value, err
	}
}

const failedValueCleanupTimeout = 30 * time.Second

func (s *Scope) construct(ctx context.Context, key Key, b binding, path []Key, root bool, owner *owner) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	allowed := make(map[Key]bool, len(b.dependencies))
	for _, dep := range b.dependencies {
		allowed[dep] = true
	}
	view := &factoryView{live: true, allowed: allowed, path: slices.Clone(path), root: root}
	value, err := invokeFactory(ctx, &Scope{state: s.state, view: view}, key, b, path)
	view.expire()
	err = errors.Join(err, ctx.Err())
	if err == nil && nilValue(value) {
		err = failure("factory", key, path, ErrNilValue, nil)
	}
	if err != nil {
		if !nilValue(value) {
			// A canceled creator must not hand an already-canceled context to
			// its failed value's cleanup. Cleanup stays synchronous and bounded.
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failedValueCleanupTimeout)
			err = errors.Join(err, closeValue(cleanupCtx, ownedValue{key: key, value: value}))
			cancel()
		}
		return nil, err
	}
	owner.mu.Lock()
	owner.values = append(owner.values, ownedValue{key: key, value: value})
	owner.mu.Unlock()
	return value, nil
}
func invokeFactory(ctx context.Context, scope *Scope, key Key, b binding, path []Key) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &Error{Operation: "factory", Key: key, Path: slices.Clone(path), Kind: ErrCallbackPanicked, Panic: recovered}
		}
	}()
	value, err = b.factory(ctx, scope)
	if err != nil {
		err = failure("factory", key, path, nil, err)
	}
	return value, err
}
