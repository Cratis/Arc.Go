// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"sync"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/serialization"
)

type executionState struct {
	mu     sync.Mutex
	top    *frame
	closed bool
}

// Execution is a callback-scoped view of synchronous command ownership. It is not
// a transaction or commit verdict. It launches no goroutines and rejects expiry.
type Execution struct {
	state      *executionState
	frame      *frame
	invocation *Invocation
}

// IsRoot reports whether this frame owns root completion.
func (e *Execution) IsRoot() bool { return e != nil && e.frame != nil && e.frame.parent == nil }

// Check verifies callback admission, current frame and security continuity.
func (e *Execution) Check(ctx context.Context) error {
	if e == nil || e.state == nil || e.invocation == nil {
		return ErrNoContext
	}
	e.invocation.mu.Lock()
	active, scope := e.invocation.active, e.invocation.scope
	e.invocation.mu.Unlock()
	if !active {
		return ErrExecutionClosed
	}
	e.state.mu.Lock()
	closed, top := e.state.closed, e.state.top
	e.state.mu.Unlock()
	if closed {
		return ErrExecutionClosed
	}
	if top != e.frame {
		return ErrExecutionMismatch
	}
	return scope.CheckContext(ctx)
}
func (f *frame) mergeNested() {
	f.owner.mu.Lock()
	if !f.nestedSet {
		f.owner.mu.Unlock()
		return
	}
	failures, err := f.nested, f.nestedErr
	f.nested, f.nestedErr, f.nestedSet = Result[NoResponse]{}, nil, false
	f.owner.mu.Unlock()
	f.merge(failures, false)
	f.err = errors.Join(f.err, err)
}

type boundPipeline struct {
	pipeline   *pipeline
	frame      *frame
	invocation *Invocation
	scope      *execution.Scope
	mu         sync.Mutex
	busy       bool
	stopped    bool
	done       chan struct{}
}

func (b *boundPipeline) Lookup(name string) (Registration, bool) { return b.pipeline.Lookup(name) }
func (b *boundPipeline) LookupCommand(command any) (Registration, error) {
	return b.pipeline.LookupCommand(command)
}
func (b *boundPipeline) Execute(ctx context.Context, command any, options ...ExecuteOptions) (Result[any], error) {
	return b.run(ctx, b.scope, command, false, options)
}
func (b *boundPipeline) Validate(ctx context.Context, command any, options ...ExecuteOptions) (Result[NoResponse], error) {
	result, err := b.run(ctx, b.scope, command, true, options)
	return NewResult(result.Details(), serialization.Optional[NoResponse]{}), err
}
func (b *boundPipeline) ExecuteScoped(ctx context.Context, scope *execution.Scope, command any, options ...ExecuteOptions) (Result[any], error) {
	return b.run(ctx, scope, command, false, options)
}
func (b *boundPipeline) ValidateScoped(ctx context.Context, scope *execution.Scope, command any, options ...ExecuteOptions) (Result[NoResponse], error) {
	result, err := b.run(ctx, scope, command, true, options)
	return NewResult(result.Details(), serialization.Optional[NoResponse]{}), err
}
func (b *boundPipeline) run(ctx context.Context, scope *execution.Scope, command any, validate bool, options []ExecuteOptions) (result Result[any], err error) {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return b.rejected(ctx, ErrExecutionClosed, validate)
	}
	if b.busy {
		b.mu.Unlock()
		return b.rejected(ctx, ErrConcurrentExecution, validate)
	}
	b.busy = true
	b.done = make(chan struct{})
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.busy = false; close(b.done); b.mu.Unlock() }()
	if err := b.invocation.owner.Check(ctx); err != nil {
		return b.rejected(ctx, err, validate)
	}
	if scope != b.scope || (b.frame.snapshot.validationOnly && !validate) {
		return b.rejected(ctx, ErrExecutionMismatch, validate)
	}
	result, err = b.pipeline.run(ctx, scope, command, validate, b.frame, options)
	if !result.IsSuccess() && (!validate || b.frame.snapshot.validationOnly) {
		b.record(result, err)
	}
	return result, err
}

// expire joins a child that the application incorrectly left running. Completion
// must not race the child. As with callbacks, joining is synchronous/cooperative.
func (b *boundPipeline) expire() error {
	b.mu.Lock()
	b.stopped = true
	busy, done := b.busy, b.done
	b.mu.Unlock()
	if busy {
		<-done
		return ErrConcurrentExecution
	}
	return nil
}
func (b *boundPipeline) rejected(ctx context.Context, err error, validate bool) (Result[any], error) {
	result := FromError[any](contextID(ctx), err)
	if !validate || b.frame.snapshot.validationOnly {
		b.record(result, err)
	}
	return result, err
}
func (b *boundPipeline) record(result Result[any], err error) {
	owner := b.frame.owner
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return
	}
	f := b.frame
	if !f.nestedSet {
		f.nested = Success(result.Details().CorrelationID)
		f.nestedSet = true
	}
	f.nested = Merge(f.nested, NewResult(result.Details(), serialization.Optional[NoResponse]{}))
	f.nestedErr = errors.Join(f.nestedErr, err)
}
