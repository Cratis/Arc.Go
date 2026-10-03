// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrCapacity rejects new IDs or unjoined operations without evicting tombstones.
	ErrCapacity = errors.New("streaming subscription capacity exhausted")
	// ErrControl rejects invalid local subscription control data.
	ErrControl = errors.New("invalid streaming subscription control")
	// ErrDraining rejects new operations during connection shutdown.
	ErrDraining = errors.New("streaming connection is draining")
)

// SubscriptionOptions bounds state retained by a physical connection.
// Zero limits default to 1024 distinct IDs and 64 outstanding operations.
type SubscriptionOptions struct{ MaxIDs, MaxOperations, MaxQueryIDBytes int }

// Operation is an internal identity token with independent cancellation. Tokens
// are compared by pointer identity. Context lifetime is connection-owned. Done
// closes only after the worker has joined its source/resources and calls Joined.
type Operation struct {
	queryID  string
	revision *Revision
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// QueryID returns the logical ID.
func (o *Operation) QueryID() string { return o.queryID }

// Revision returns a copy; nil means legacy.
func (o *Operation) Revision() *Revision { return copyRevision(o.revision) }

// Context is canceled by replacement, unsubscribe or connection drain.
func (o *Operation) Context() context.Context { return o.ctx }

// Done signals actual cleanup joining, not just cancellation.
func (o *Operation) Done() <-chan struct{} { return o.done }

type subscriptionState struct {
	revision *Revision
	current  *Operation
}

// Subscriptions owns revision high-water marks for one physical connection.
// Tombstones are never evicted while it lives. Replaced but unjoined workers
// count toward capacity. No callback or network operation runs under its mutex.
type Subscriptions struct {
	mu         sync.Mutex
	ctx        context.Context
	options    SubscriptionOptions
	states     map[string]subscriptionState
	operations map[*Operation]struct{}
	draining   bool
}

// NewSubscriptions validates copied limits without starting work.
func NewSubscriptions(ctx context.Context, options SubscriptionOptions) (*Subscriptions, error) {
	if ctx == nil || options.MaxIDs < 0 || options.MaxOperations < 0 || options.MaxQueryIDBytes < 0 {
		return nil, ErrControl
	}
	if options.MaxIDs == 0 {
		options.MaxIDs = 1024
	}
	if options.MaxOperations == 0 {
		options.MaxOperations = 64
	}
	if options.MaxQueryIDBytes == 0 {
		options.MaxQueryIDBytes = 256
	}
	return &Subscriptions{ctx: ctx, options: options, states: map[string]subscriptionState{}, operations: map[*Operation]struct{}{}}, nil
}
func (s *Subscriptions) validate(id string, revision *Revision) error {
	if id == "" || len(id) > s.options.MaxQueryIDBytes {
		return ErrControl
	}
	if revision != nil && !revision.Valid() {
		return ErrRevision
	}
	return nil
}

// Subscribe reserves one opening owner, or returns nil for duplicate/stale controls.
// replaced is canceled but remains capacity-counted until Joined. When replacing
// a legacy owner, the transport must fence its writer before activating the new
// worker, so an already-started old frame cannot trail the successor's first frame.
func (s *Subscriptions) Subscribe(id string, revision *Revision) (operation, replaced *Operation, err error) {
	if err := s.validate(id, revision); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	if s.draining || s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, nil, ErrDraining
	}
	state, exists := s.states[id]
	if state.revision != nil && (revision == nil || *revision <= *state.revision) {
		s.mu.Unlock()
		return nil, nil, nil
	}
	if !exists && len(s.states) >= s.options.MaxIDs || len(s.operations) >= s.options.MaxOperations {
		s.mu.Unlock()
		return nil, nil, ErrCapacity
	}
	ctx, cancel := context.WithCancel(s.ctx)
	operation = &Operation{queryID: id, revision: copyRevision(revision), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	replaced = state.current
	s.states[id] = subscriptionState{revision: copyRevision(revision), current: operation}
	s.operations[operation] = struct{}{}
	s.mu.Unlock()
	if replaced != nil {
		replaced.cancel()
	}
	return operation, replaced, nil
}

// Unsubscribe accepts an equal current revision as required by the actual JS
// client. Greater revisions tombstone even absent IDs; stale/legacy controls on
// revision-aware IDs are compatible no-ops. The caller still owns worker joining.
func (s *Subscriptions) Unsubscribe(id string, revision *Revision) error {
	if err := s.validate(id, revision); err != nil {
		return err
	}
	s.mu.Lock()
	if s.draining {
		s.mu.Unlock()
		return ErrDraining
	}
	state, exists := s.states[id]
	if state.revision != nil && (revision == nil || *revision < *state.revision) {
		s.mu.Unlock()
		return nil
	}
	if !exists && revision == nil {
		s.mu.Unlock()
		return nil
	}
	if !exists && len(s.states) >= s.options.MaxIDs {
		s.mu.Unlock()
		return ErrCapacity
	}
	old := state.current
	if revision == nil {
		delete(s.states, id)
	} else {
		s.states[id] = subscriptionState{revision: copyRevision(revision)}
	}
	s.mu.Unlock()
	if old != nil {
		old.cancel()
	}
	return nil
}

// Owns gates opening, emission, error and termination by token identity. Checking
// before a write cannot retract an already-started network write; revisions let
// aware clients discard that race, while legacy replacement requires a fence.
func (s *Subscriptions) Owns(o *Operation) bool {
	if o == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.draining && s.ctx.Err() == nil && s.states[o.queryID].current == o && o.ctx.Err() == nil
}

// Terminate retires only the current owner, preserving a revision tombstone.
// It does not claim workers/resources are joined. Late terminal errors from old
// owners cannot remove their successor. Repeated calls are harmless.
func (s *Subscriptions) Terminate(o *Operation) {
	if o == nil {
		return
	}
	s.mu.Lock()
	if _, exists := s.operations[o]; !exists {
		s.mu.Unlock()
		return
	}
	if state, exists := s.states[o.queryID]; exists && state.current == o {
		if state.revision == nil {
			delete(s.states, o.queryID)
		} else {
			state.current = nil
			s.states[o.queryID] = state
		}
	}
	s.mu.Unlock()
	o.cancel()
}

// Joined records that all work and resource cleanup for this token has ended.
// Call only after actual joining, never after a close timeout. Repeated calls are
// safe; this also retires a current completed owner without affecting successors.
func (s *Subscriptions) Joined(o *Operation) {
	if o == nil {
		return
	}
	s.Terminate(o)
	s.mu.Lock()
	if _, exists := s.operations[o]; exists {
		delete(s.operations, o)
		close(o.done)
	}
	s.mu.Unlock()
}

// Drain stops admission and cancels all opening, active and retired operations.
// Returned tokens must each be joined by the owning connection. State remains
// available for later joins when shutdown times out; no tombstone is evicted.
func (s *Subscriptions) Drain() []*Operation {
	s.mu.Lock()
	s.draining = true
	list := make([]*Operation, 0, len(s.operations))
	for o := range s.operations {
		list = append(list, o)
	}
	s.mu.Unlock()
	for _, o := range list {
		o.cancel()
	}
	return list
}

// Counts reports retained IDs and outstanding (including retired) operations.
func (s *Subscriptions) Counts() (ids, operations int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.states), len(s.operations)
}
