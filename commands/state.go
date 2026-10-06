// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import "context"

// StateKey identifies extension-owned state. Construct it with NewStateKey; zero
// is invalid. Copies identify the same slot. Keep ownership-capability keys private.
type StateKey[T any] struct {
	identity *stateIdentity
	_        *T // Prevent explicit conversion between keys of different value types.
}

type stateIdentity struct{ _ byte }

// NewStateKey allocates an independent typed slot, without process-wide storage.
func NewStateKey[T any]() StateKey[T] { return StateKey[T]{identity: &stateIdentity{}} }

// RootState reads a slot shared only by this root's bound nested command tree.
// Values are borrowed, not cloned: their owner must guard mutable contents and
// recheck Invocation.Execution before using a retained capability. Membership is
// discarded at execution end. Every access checks callback/security continuity.
func RootState[T any](ctx context.Context, inv *Invocation, key StateKey[T]) (T, bool, error) {
	return getState(ctx, inv, key, true)
}

// SetRootState replaces a root slot. No initializer or user callback runs under
// the state lock. It does not confer completion authority on an operation scope.
func SetRootState[T any](ctx context.Context, inv *Invocation, key StateKey[T], value T) error {
	return setState(ctx, inv, key, value, true)
}

// FrameState reads a slot shared across one command's callbacks, never siblings
// or children. Membership ends with that command. Values follow RootState ownership.
func FrameState[T any](ctx context.Context, inv *Invocation, key StateKey[T]) (T, bool, error) {
	return getState(ctx, inv, key, false)
}

// SetFrameState replaces a frame-local slot after checking callback admission.
func SetFrameState[T any](ctx context.Context, inv *Invocation, key StateKey[T], value T) error {
	return setState(ctx, inv, key, value, false)
}

func getState[T any](ctx context.Context, inv *Invocation, key StateKey[T], root bool) (value T, found bool, err error) {
	if key.identity == nil {
		return value, false, ErrInvalidRegistration
	}
	err = withState(ctx, inv, func(e *Execution) error {
		state := e.frame.state
		if root {
			state = e.state.values
		}
		stored, ok := state[key.identity]
		if ok {
			// The box preserves a present nil interface value of T.
			value, found = stored.(stateValue[T]).value, true
		}
		return nil
	})
	return
}

type stateValue[T any] struct{ value T }

func setState[T any](ctx context.Context, inv *Invocation, key StateKey[T], value T, root bool) error {
	if key.identity == nil {
		return ErrInvalidRegistration
	}
	return withState(ctx, inv, func(e *Execution) error {
		state := &e.frame.state
		if root {
			state = &e.state.values
		}
		if *state == nil {
			*state = make(map[*stateIdentity]any)
		}
		(*state)[key.identity] = stateValue[T]{value}
		return nil
	})
}

// withState calls only internal, nonblocking code while holding the locks. Scope
// continuity may invoke a user ContextChecker, so that check precedes the locks.
func withState(ctx context.Context, inv *Invocation, call func(*Execution) error) error {
	if inv == nil || inv.owner == nil {
		return ErrNoContext
	}
	e := inv.owner
	if err := e.Check(ctx); err != nil {
		return err
	}
	inv.mu.Lock()
	defer inv.mu.Unlock()
	e.state.mu.Lock()
	defer e.state.mu.Unlock()
	if !inv.active || e.state.closed || e.frame.ended {
		return ErrExecutionClosed
	}
	if e.state.top != e.frame {
		return ErrExecutionMismatch
	}
	return call(e)
}

// ParentCommandContext returns the parent's metadata at nested admission, if
// present. Membership and descriptors are copied; command/value objects remain
// borrowed immutable data. It exposes no parent invocation or completion owner.
func (i *Invocation) ParentCommandContext(ctx context.Context) (value CommandContext, present bool, err error) {
	err = withState(ctx, i, func(e *Execution) error {
		if e.frame.parent != nil {
			value, present = e.frame.parentContext, true
			value.values = value.Values()
		}
		return nil
	})
	return
}
