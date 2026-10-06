// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"errors"
	"sync"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

var (
	// ErrInvalidScope identifies an unconstructed scope.
	ErrInvalidScope = errors.New("invalid operation scope")
	// ErrScopeClosed identifies a scope that no longer admits work.
	ErrScopeClosed = errors.New("operation scope closed")
	// ErrScopeExpired identifies a retained callback view.
	ErrScopeExpired = errors.New("operation scope view expired")
	// ErrScopeJoinPending means Close has stopped admission but owned work has
	// not joined. Retry Close with a fresh budget; disposal is never restarted.
	ErrScopeJoinPending = errors.New("operation scope cleanup join pending")
	// ErrScopeView identifies an attempt to close a non-owning callback view.
	ErrScopeView = errors.New("operation scope view cannot close resources")
	// ErrIdentityChanged identifies changed principal/tenant values or presence.
	ErrIdentityChanged = errors.New("operation scope identity changed")
)

// Scope guards one operation's security and resource lifetime. Zero is invalid.
// It is not an authorization verdict or transaction. Do not copy a Scope.
// Methods are concurrent-safe; application resources need their own concurrency contract.
// Use supplies non-closing, expiring views. No method starts a goroutine.
type Scope struct {
	state *scopeState
	view  *scopeView
}

type scopeView struct {
	active bool // guarded by state.mu
	parent *scopeView
}

type scopeState struct {
	mu                              sync.Mutex
	resources                       Resources
	owned                           bool
	principal                       identity.Principal
	tenant                          tenancy.ID
	principalPresent, tenantPresent bool
	uses                            int
	idle                            chan struct{}
	closing, disposing, closed      bool
	done                            chan struct{}
	closeErr                        error
	disposalStarted                 bool
	disposalErr                     error
	joinErr                         error
}

func newScope(ctx context.Context, owned bool) *Scope {
	principal, pp := identity.PrincipalFrom(ctx)
	tenant, tp := tenancy.TenantFrom(ctx)
	idle := make(chan struct{})
	close(idle)
	return &Scope{state: &scopeState{owned: owned, principal: principal, tenant: tenant,
		principalPresent: pp, tenantPresent: tp, idle: idle, done: make(chan struct{})}}
}

// CheckContext checks cancellation, security presence and lifetime. An admitted
// view remains usable while Close waits, but expires when its callback returns.
// Correlation and receipt changes do not change security ownership. Resources
// implementing di.ContextChecker are checked too, including borrowed DI scopes.
func (s *Scope) CheckContext(ctx context.Context) error {
	if s == nil || s.state == nil {
		return ErrInvalidScope
	}
	if ctx == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := s.state
	state.mu.Lock()
	err := s.checkLifetime()
	state.mu.Unlock()
	if err != nil {
		return err
	}
	principal, pp := identity.PrincipalFrom(ctx)
	tenant, tp := tenancy.TenantFrom(ctx)
	if pp != state.principalPresent || tp != state.tenantPresent || !state.principal.Equal(principal) || tenant != state.tenant {
		return ErrIdentityChanged
	}
	if checker, ok := state.resources.(di.ContextChecker); ok {
		return invoke(ctx, func() error { return checker.CheckContext(ctx) })
	}
	return nil
}

// checkLifetime requires state.mu.
func (s *Scope) checkLifetime() error {
	for view := s.view; view != nil; view = view.parent {
		if !view.active {
			return ErrScopeExpired
		}
	}
	if s.state.closed || (s.view == nil && s.state.closing) {
		return ErrScopeClosed
	}
	return nil
}

// Use admits a synchronous callback, checking cancellation/security before and
// after it. Panics become PanicError. The supplied view cannot close resources
// and expires on return. Retained resources themselves cannot be revoked.
func (s *Scope) Use(ctx context.Context, call func(context.Context, *Scope) error) (err error) {
	if call == nil {
		return ErrInvalidArgument
	}
	if err := s.CheckContext(ctx); err != nil {
		return err
	}
	state := s.state
	state.mu.Lock()
	if err := s.checkLifetime(); err != nil {
		state.mu.Unlock()
		return err
	}
	if state.closing {
		state.mu.Unlock()
		return ErrScopeClosed
	}
	if state.uses == 0 {
		state.idle = make(chan struct{})
	}
	state.uses++
	view := &Scope{state: state, view: &scopeView{active: true, parent: s.view}}
	state.mu.Unlock()
	defer func() {
		state.mu.Lock()
		view.view.active = false
		state.uses--
		if state.uses == 0 {
			close(state.idle)
		}
		state.mu.Unlock()
	}()
	err = invoke(ctx, func() error { return call(ctx, view) })
	return errors.Join(err, view.CheckContext(ctx))
}

// Close stops admission and joins admitted uses before closing owned resources
// at most once. Borrowed resources are never closed. Repeated calls return the
// recorded disposal outcome. A canceled admitted-use join leaves admission closed.
// ResourcesJoiner permits later calls to resume only the cleanup join, never Close
// initiation. Incomplete owned joins return PendingScopeError wrapping
// ErrScopeJoinPending; ordinary resource failures
// (including context errors without that capability) are final and cached.
// Cleanup is synchronous and cooperative. Never call
// an owning scope's Close from inside one of its own admitted callbacks.
func (s *Scope) Close(ctx context.Context) (err error) {
	if s == nil || s.state == nil {
		return ErrInvalidScope
	}
	if s.view != nil {
		return ErrScopeView
	}
	if ctx == nil {
		return ErrInvalidArgument
	}
	state := s.state
	// Wrap only the returned diagnostic, never the cached state: retaining a
	// PendingScopeError in state.closeErr would create a recursive error graph.
	defer func() {
		if state.owned && errors.Is(err, ErrScopeJoinPending) {
			err = &PendingScopeError{scope: s, err: err}
		}
	}()
	state.mu.Lock()
	if state.closed {
		err := state.closeErr
		state.mu.Unlock()
		return err
	}
	state.closing = true
	idle := state.idle
	state.mu.Unlock()
	select {
	case <-idle:
	case <-ctx.Done():
		return errors.Join(ErrScopeJoinPending, ctx.Err())
	}
	state.mu.Lock()
	if state.closed {
		err := state.closeErr
		state.mu.Unlock()
		return err
	}
	if state.disposing {
		done := state.done
		state.mu.Unlock()
		select {
		case <-done:
			state.mu.Lock()
			err := state.closeErr
			state.mu.Unlock()
			return err
		case <-ctx.Done():
			return errors.Join(ErrScopeJoinPending, ctx.Err())
		}
	}
	if err := ctx.Err(); err != nil {
		state.mu.Unlock()
		return errors.Join(ErrScopeJoinPending, err)
	}
	state.disposing = true
	state.done = make(chan struct{})
	initiate := !state.disposalStarted
	state.disposalStarted = true
	state.mu.Unlock()
	joiner, resumable := state.resources.(ResourcesJoiner)
	resumable = resumable && state.owned
	if initiate && state.owned && state.resources != nil {
		// Once initiation is recorded, call Close even if cancellation races
		// this point. The holder still receives the original bounded context.
		err := invoke(context.WithoutCancel(ctx), func() error { return state.resources.Close(ctx) })
		err = errors.Join(err, ctx.Err())
		if resumable {
			// Only an explicit join capability makes context errors provisional.
			// Preserve other failures even when joined with a timeout.
			err = withoutWaitErrors(err)
		}
		state.disposalErr = err // This attempt exclusively owns cleanup state.
	}
	err = errors.Join(state.disposalErr, state.joinErr)
	pending := false
	if resumable {
		returned := false
		joinErr := invoke(ctx, func() error {
			err := joiner.Join(ctx)
			returned = true
			return err
		})
		// A returned non-context failure certifies completion even if its
		// diagnostic is PanicError; only a recovered callback panic is unknown.
		pending = !returned || errors.Is(joinErr, context.Canceled) || errors.Is(joinErr, context.DeadlineExceeded)
		// Retain non-waiting diagnostics from every attempt. A later successful
		// Join resolves waiting, not an earlier domain failure or panic.
		state.joinErr = errors.Join(state.joinErr, withoutWaitErrors(joinErr))
		err = errors.Join(err, joinErr)
		if pending {
			err = errors.Join(ErrScopeJoinPending, err)
		}
	}
	state.mu.Lock()
	state.closeErr = err
	state.closed = !pending
	state.disposing = false
	close(state.done)
	state.mu.Unlock()
	return err
}

// Strip only pure waiting leaves. A wrapped or joined non-context failure must
// survive; arbitrary Resources.Close errors never pass through this helper.
func withoutWaitErrors(err error) error {
	if err == context.Canceled || err == context.DeadlineExceeded {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var remaining error
		for _, part := range joined.Unwrap() {
			remaining = errors.Join(remaining, withoutWaitErrors(part))
		}
		if remaining == nil {
			return nil
		}
		return &waitFilteredError{original: err, remaining: remaining}
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		remaining := withoutWaitErrors(wrapped.Unwrap())
		if remaining == nil {
			return nil
		}
		return &waitFilteredError{original: err, remaining: remaining}
	}
	return err
}

// Preserve wrapper identities and typed diagnostics while exposing only the
// non-waiting tree to errors.Is. Unwrap never points back to original.
type waitFilteredError struct {
	original, remaining error
}

func (e *waitFilteredError) Error() string { return e.remaining.Error() }
func (e *waitFilteredError) Unwrap() error { return e.remaining }
func (e *waitFilteredError) Is(target error) bool {
	return target != context.Canceled && target != context.DeadlineExceeded && errors.Is(e.original, target)
}
func (e *waitFilteredError) As(target any) bool { return errors.As(e.original, target) }
