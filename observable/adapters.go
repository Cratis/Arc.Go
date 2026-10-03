// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observable

import (
	"context"
	"errors"
	"io"
	"iter"
	"sync"
)

type producerSource[T any] struct {
	run func(context.Context, func(T) error) error
}
type producerStream[T any] struct {
	ctx      context.Context
	cancel   context.CancelFunc
	values   chan T
	done     chan struct{}
	err      error // published by closing done
	consumer consumer
}

// FromProducer defers activation to Open. The stream owns one worker; Close
// cancels and joins it. The producer must honor cancellation and join any work it
// creates before returning. Concurrent emit calls are serialized. Values transfer
// immutably; a producer must not mutate a value after emit returns. Callback use
// after run returns is unsupported. Producer errors remain locally inspectable.
func FromProducer[T any](run func(context.Context, func(T) error) error) Source[T] {
	return producerSource[T]{run: run}
}
func (p producerSource[T]) Open(ctx context.Context) (Stream[T], error) {
	if ctx == nil || p.run == nil {
		return nil, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	work, cancel := context.WithCancel(ctx)
	s := &producerStream[T]{ctx: work, cancel: cancel, values: make(chan T), done: make(chan struct{})}
	// The handle owns this worker. Cancellation wakes emit; done joins the
	// callback and all producer-owned cleanup, including a blocked startup.
	go func() {
		defer close(s.done)
		defer func() {
			if recover() != nil {
				s.err = ErrProducerPanic
			}
		}()
		gate := make(chan struct{}, 1)
		s.err = p.run(work, func(value T) error {
			if err := acquire(work, gate); err != nil {
				return err
			}
			defer func() { <-gate }()
			select {
			case <-work.Done():
				return work.Err()
			case s.values <- value:
				return nil
			}
		})
	}()
	return s, nil
}
func (s *producerStream[T]) Next(ctx context.Context) (T, error) {
	var zero T
	if ctx == nil {
		return zero, ErrInvalidOptions
	}
	if err := s.consumer.enter(); err != nil {
		return zero, err
	}
	defer s.consumer.leave()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := s.ctx.Err(); err != nil {
		return zero, err
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-s.ctx.Done():
		return zero, s.ctx.Err()
	case value := <-s.values:
		return value, nil
	case <-s.done:
		if s.err != nil {
			return zero, s.err
		}
		return zero, io.EOF
	}
}
func (s *producerStream[T]) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	active := s.consumer.stop()
	s.cancel()
	if err := join(ctx, s.done); err != nil {
		return err
	}
	if err := join(ctx, active); err != nil {
		return err
	}
	if errors.Is(s.err, context.Canceled) {
		return nil
	}
	return s.err
}

// FromIterator opens and consumes one iterator per stream. Returning false from
// yield stops iteration, so iterator cleanup must run on early cancellation.
// A blocking iterator must honor its supplied context; Close never detaches it.
func FromIterator[T any](open func(context.Context) iter.Seq2[T, error]) Source[T] {
	return FromProducer(func(ctx context.Context, emit func(T) error) error {
		if open == nil {
			return ErrInvalidOptions
		}
		sequence := open(ctx)
		if sequence == nil {
			return ErrInvalidOptions
		}
		var failure error
		sequence(func(value T, err error) bool {
			if err != nil {
				failure = err
				return false
			}
			failure = emit(value)
			return failure == nil
		})
		return failure
	})
}

// ChannelFactory opens a borrowed channel and returns a cleanup callback that
// must cancel and join its producer. The callback must support repeated calls
// after a timed-out attempt. A nonnil cleanup callback transfers ownership even
// when startup fails or returns an invalid channel: Open returns a nonnil stream
// alongside the error, and the caller must Close it with a separate cleanup budget.
// Arc never closes the borrowed channel. Closing the channel means normal
// completion; producer failures should use FromProducer.
type ChannelFactory[T any] func(context.Context) (<-chan T, func(context.Context) error, error)

type channelSource[T any] struct{ factory ChannelFactory[T] }
type channelStream[T any] struct {
	ctx      context.Context
	cancel   context.CancelFunc
	values   <-chan T
	cleanup  func(context.Context) error
	gate     chan struct{}
	mu       sync.Mutex
	closed   bool
	consumer consumer
}

// FromChannelFactory constructs a lazy source with explicit producer cleanup.
func FromChannelFactory[T any](factory ChannelFactory[T]) Source[T] {
	return channelSource[T]{factory: factory}
}
func (p channelSource[T]) Open(ctx context.Context) (Stream[T], error) {
	if ctx == nil || p.factory == nil {
		return nil, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	work, cancel := context.WithCancel(ctx)
	values, cleanup, err := p.factory(work)
	err = errors.Join(err, work.Err())
	if cleanup == nil {
		cancel()
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidOptions
	}
	stream := &channelStream[T]{ctx: work, cancel: cancel, values: values, cleanup: cleanup, gate: make(chan struct{}, 1)}
	if values == nil {
		err = errors.Join(err, ErrInvalidOptions)
	}
	if err != nil {
		cancel() // Stop startup work, but retain its handle until cleanup joins.
	}
	return stream, err
}
func (s *channelStream[T]) Next(ctx context.Context) (T, error) {
	var zero T
	if ctx == nil {
		return zero, ErrInvalidOptions
	}
	if err := s.consumer.enter(); err != nil {
		return zero, err
	}
	defer s.consumer.leave()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := s.ctx.Err(); err != nil {
		return zero, err
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-s.ctx.Done():
		return zero, s.ctx.Err()
	case value, ok := <-s.values:
		if !ok {
			return zero, io.EOF
		}
		return value, nil
	}
}
func (s *channelStream[T]) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	active := s.consumer.stop()
	s.cancel()
	if err := acquire(ctx, s.gate); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if !closed {
		if err := s.cleanup(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}
	return join(ctx, active)
}
