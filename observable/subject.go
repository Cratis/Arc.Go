// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observable

import (
	"context"
	"io"
	"sync"
)

// SubjectOptions bounds each subscriber's queue. Buffer zero defaults to 16;
// negative values are rejected. Clone must detach nested mutable values and be
// concurrent-safe. It runs outside framework locks. Without Clone, values must
// remain immutable after publishing, including after completion.
type SubjectOptions[T any] struct {
	Buffer int
	Clone  func(T) (T, error)
}

// Subject is a concurrent-safe multicast source without current-value replay.
// Use NewSubject; the zero value is not initialized. Complete and Fail terminate
// subscribers, while closing one stream affects only that subscription.
type Subject[T any] struct{ core *subjectCore[T] }

// State is a concurrent-safe subject with atomic current-value attachment/replay.
// Use NewState or NewPendingState. Nil and zero initial values are current values.
type State[T any] struct{ core *subjectCore[T] }

type subjectCore[T any] struct {
	mu       sync.Mutex
	gate     chan struct{}
	options  SubjectOptions[T]
	stateful bool
	current  T
	present  bool
	terminal error
	subs     map[*subjectStream[T]]struct{}
}
type subjectStream[T any] struct {
	core     *subjectCore[T]
	ctx      context.Context
	values   chan T
	done     chan struct{}
	closed   bool  // protected by core.mu
	terminal error // protected by core.mu
	consumer consumer
}

func newCore[T any](options SubjectOptions[T], stateful bool) (*subjectCore[T], error) {
	if options.Buffer < 0 {
		return nil, ErrInvalidOptions
	}
	if options.Buffer == 0 {
		options.Buffer = 16
	}
	return &subjectCore[T]{gate: make(chan struct{}, 1), options: options, stateful: stateful, subs: make(map[*subjectStream[T]]struct{})}, nil
}

// NewSubject constructs a subject without activating any work.
func NewSubject[T any](options SubjectOptions[T]) (*Subject[T], error) {
	core, err := newCore(options, false)
	if err != nil {
		return nil, err
	}
	return &Subject[T]{core: core}, nil
}

// NewPendingState constructs a state with no current value.
func NewPendingState[T any](options SubjectOptions[T]) (*State[T], error) {
	core, err := newCore(options, true)
	if err != nil {
		return nil, err
	}
	return &State[T]{core: core}, nil
}

// NewState clones initial if configured. Construction performs no I/O or work.
func NewState[T any](initial T, options SubjectOptions[T]) (*State[T], error) {
	s, err := NewPendingState(options)
	if err != nil {
		return nil, err
	}
	if options.Clone != nil {
		initial, err = options.Clone(initial)
		if err != nil {
			return nil, err
		}
	}
	s.core.current, s.core.present = initial, true
	return s, nil
}

// Open attaches one subscriber. Closing it never affects the subject.
func (s *Subject[T]) Open(ctx context.Context) (Stream[T], error) {
	if s == nil || s.core == nil {
		return nil, ErrInvalidOptions
	}
	return s.core.open(ctx)
}

// Open attaches and queues the current value atomically with publication.
func (s *State[T]) Open(ctx context.Context) (Stream[T], error) {
	if s == nil || s.core == nil {
		return nil, ErrInvalidOptions
	}
	return s.core.open(ctx)
}
func (c *subjectCore[T]) open(ctx context.Context) (Stream[T], error) {
	if ctx == nil {
		return nil, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal != nil && c.terminal != io.EOF {
		return nil, c.terminal
	}
	s := &subjectStream[T]{core: c, ctx: ctx, values: make(chan T, c.options.Buffer), done: make(chan struct{})}
	if c.present {
		s.values <- c.current
	}
	if c.terminal != nil {
		s.terminate(c.terminal)
	} else {
		c.subs[s] = struct{}{}
	}
	return s, nil
}

// Current returns a detached current value when configured, preserving presence.
func (s *State[T]) Current(ctx context.Context) (T, bool, error) {
	var zero T
	if s == nil || s.core == nil || ctx == nil {
		return zero, false, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return zero, false, err
	}
	c := s.core
	c.mu.Lock()
	value, present, terminal := c.current, c.present, c.terminal
	c.mu.Unlock()
	if terminal != nil && terminal != io.EOF {
		return zero, false, terminal
	}
	if !present {
		return zero, false, nil
	}
	if c.options.Clone != nil {
		var err error
		value, err = c.options.Clone(value)
		if err != nil {
			return zero, false, err
		}
	}
	return value, true, nil
}

// Publish serializes concurrent publishers and never waits for a slow subscriber.
// An overflow terminates only that subscriber and returns a partial PublishError.
func (s *Subject[T]) Publish(ctx context.Context, value T) error {
	if s == nil || s.core == nil {
		return ErrInvalidOptions
	}
	return s.core.publish(ctx, value)
}

// Publish replaces the current immutable snapshot and broadcasts it.
func (s *State[T]) Publish(ctx context.Context, value T) error {
	if s == nil || s.core == nil {
		return ErrInvalidOptions
	}
	return s.core.publish(ctx, value)
}
func (c *subjectCore[T]) publish(ctx context.Context, value T) error {
	if err := acquire(ctx, c.gate); err != nil {
		return err
	}
	defer func() { <-c.gate }()
	if c.options.Clone != nil {
		var err error
		value, err = c.options.Clone(value)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal != nil {
		return ErrClosed
	}
	if c.stateful {
		c.current, c.present = value, true
	}
	var report PublishError
	for s := range c.subs {
		if err := s.ctx.Err(); err != nil {
			s.terminate(err)
			delete(c.subs, s)
			continue
		}
		select {
		case s.values <- value:
			report.Delivered++
		default:
			s.terminate(ErrOverflow)
			delete(c.subs, s)
			report.Overflowed++
		}
	}
	if report.Overflowed != 0 {
		return &report
	}
	return nil
}

// Complete closes the subject normally. Queued values precede io.EOF.
func (s *Subject[T]) Complete() error {
	if s == nil || s.core == nil {
		return ErrInvalidOptions
	}
	return s.core.finish(io.EOF)
}

// Complete retains the last current value, if any, and completes subscriptions.
func (s *State[T]) Complete() error {
	if s == nil || s.core == nil {
		return ErrInvalidOptions
	}
	return s.core.finish(io.EOF)
}

// Fail terminates subscriptions with err, discarding buffered stale values.
// Nil and io.EOF are invalid failures. Repeating the same terminal action is safe.
func (s *Subject[T]) Fail(err error) error {
	if s == nil || s.core == nil || err == nil || err == io.EOF {
		return ErrInvalidOptions
	}
	return s.core.finish(err)
}

// Fail prevents subsequent reads from presenting a stale successful current value.
func (s *State[T]) Fail(err error) error {
	if s == nil || s.core == nil || err == nil || err == io.EOF {
		return ErrInvalidOptions
	}
	return s.core.finish(err)
}
func (c *subjectCore[T]) finish(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal != nil {
		return nil
	}
	c.terminal = err
	for s := range c.subs {
		s.terminate(err)
		delete(c.subs, s)
	}
	return nil
}
func (s *subjectStream[T]) terminate(err error) {
	if s.closed {
		return
	}
	s.closed, s.terminal = true, err
	close(s.done)
}
func (s *subjectStream[T]) Next(ctx context.Context) (T, error) {
	var zero T
	if ctx == nil {
		return zero, ErrInvalidOptions
	}
	if err := s.consumer.enter(); err != nil {
		return zero, err
	}
	defer s.consumer.leave()
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if err := s.ctx.Err(); err != nil {
			return zero, err
		}
		s.core.mu.Lock()
		terminal := s.terminal
		s.core.mu.Unlock()
		if terminal != nil && terminal != io.EOF {
			return zero, terminal
		}
		// Preserve accepted emissions ahead of normal completion.
		select {
		case value := <-s.values:
			return s.clone(value)
		default:
		}
		if terminal != nil {
			return zero, terminal
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-s.ctx.Done():
			return zero, s.ctx.Err()
		case <-s.done:
			continue
		case value := <-s.values:
			s.core.mu.Lock()
			terminal = s.terminal
			s.core.mu.Unlock()
			if terminal != nil && terminal != io.EOF {
				return zero, terminal
			}
			return s.clone(value)
		}
	}
}
func (s *subjectStream[T]) clone(value T) (T, error) {
	if s.core.options.Clone != nil {
		return s.core.options.Clone(value)
	}
	return value, nil
}
func (s *subjectStream[T]) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	active := s.consumer.stop()
	s.core.mu.Lock()
	if !s.closed {
		s.terminate(ErrClosed)
	}
	delete(s.core.subs, s)
	s.core.mu.Unlock()
	return join(ctx, active)
}
