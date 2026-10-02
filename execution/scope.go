// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution

import (
	"context"
	"errors"
	"sync"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

var (
	// ErrInvalidScope identifies an unconstructed scope.
	ErrInvalidScope = errors.New("invalid operation scope")
	// ErrScopeClosed identifies a scope that no longer admits work.
	ErrScopeClosed = errors.New("operation scope closed")
	// ErrScopeExpired identifies a retained callback view.
	ErrScopeExpired = errors.New("operation scope view expired")
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
// Correlation and receipt changes do not change security ownership.
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
// recorded disposal outcome. A canceled join leaves admission closed; a later
// Close must finish disposal. Cleanup is synchronous and cooperative. Never call
// an owning scope's Close from inside one of its own admitted callbacks.
func (s *Scope) Close(ctx context.Context) error {
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
		return ctx.Err()
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
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		state.mu.Unlock()
		return err
	}
	state.disposing = true
	state.mu.Unlock()
	var err error
	if state.owned && state.resources != nil {
		err = invoke(ctx, func() error { return state.resources.Close(ctx) })
	}
	state.mu.Lock()
	state.closeErr = err
	state.closed = true
	close(state.done)
	state.mu.Unlock()
	return err
}
