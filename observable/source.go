// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package observable provides independently owned query streams. Publishing
// transfers immutable values unless SubjectOptions.Clone is supplied. Closing a
// stream never completes its shared source. No source construction starts work.
package observable

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Source opens one independently owned stream after admission. Callers must Close
// even after exhaustion or cancellation. Implementations must honor cancellation.
type Source[T any] interface {
	Open(context.Context) (Stream[T], error)
}

// Stream has a single consumer. Next returns io.EOF only for normal completion;
// zero and nil values are real emissions. Close cancels and joins owned work.
// Repeated Close is safe; a timed-out Close may be continued with a later budget.
// Context errors or ErrJoinPending mean joining is incomplete or unknown; any
// other returned result certifies completion, including a final failure.
type Stream[T any] interface {
	Next(context.Context) (T, error)
	Close(context.Context) error
}

// CurrentSource distinguishes absence from a current zero or nil value.
// A completed source may expose its last value; a failed source returns its error.
type CurrentSource[T any] interface {
	Source[T]
	Current(context.Context) (T, bool, error)
}

var (
	// ErrJoinPending means stream cleanup completion is unknown or incomplete.
	// Retain the stream and continue Close with a fresh budget; do not dispose
	// dependencies beneath its producer. Continuation must not repeat side effects.
	ErrJoinPending = errors.New("observable cleanup join pending")
	// ErrClosed means the source or stream has been closed.
	ErrClosed = errors.New("observable is closed")
	// ErrConcurrentNext rejects overlapping consumption of the same stream.
	ErrConcurrentNext = errors.New("observable Next is already running")
	// ErrOverflow terminates a slow subscriber without affecting other subscribers.
	ErrOverflow = errors.New("observable subscriber buffer exhausted")
	// ErrInvalidOptions rejects invalid construction or operation arguments.
	ErrInvalidOptions = errors.New("invalid observable options")
	// ErrProducerPanic reports a recovered producer panic without exposing its value.
	ErrProducerPanic = errors.New("observable producer panicked")
)

// PublishError describes partial publishing. Delivered subscribers accepted the
// immutable value; Overflowed subscribers terminated. Do not blindly retry.
type PublishError struct {
	Delivered  int
	Overflowed int
}

func (e *PublishError) Error() string {
	return fmt.Sprintf("observable publish: %d accepted, %d overflowed", e.Delivered, e.Overflowed)
}

// Unwrap permits errors.Is(err, ErrOverflow).
func (*PublishError) Unwrap() error { return ErrOverflow }

// consumer coordinates single Next ownership and Close joining without workers.
type consumer struct {
	busy    atomic.Bool
	mu      sync.Mutex
	closing bool
	active  chan struct{}
}

func (c *consumer) enter() error {
	if !c.busy.CompareAndSwap(false, true) {
		return ErrConcurrentNext
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		c.busy.Store(false)
		return ErrClosed
	}
	c.active = make(chan struct{})
	return nil
}
func (c *consumer) leave() {
	c.mu.Lock()
	close(c.active)
	c.active = nil
	c.busy.Store(false)
	c.mu.Unlock()
}
func (c *consumer) stop() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closing = true
	return c.active
}
func join(ctx context.Context, done <-chan struct{}) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	if done == nil {
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func acquire(ctx context.Context, gate chan struct{}) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
