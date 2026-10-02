// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import (
	"context"
	"sync"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

// Scope owns scoped values and its transient instances. Zero is invalid.
// Ordinary handles support concurrent resolution. Factory views authorize declared
// direct edges only, cannot be used concurrently, and expire when the factory returns.
// Resolved services must independently support any concurrent application use.
type Scope struct {
	state *scopeState
	view  *factoryView
}
type scopeState struct {
	provider *Provider
	owner    *owner
	security securitySnapshot
}
type securitySnapshot struct {
	principal        identity.Principal
	principalPresent bool
	tenant           tenancy.ID
	tenantPresent    bool
}

func captureSecurity(ctx context.Context) securitySnapshot {
	p, pp := identity.PrincipalFrom(ctx)
	t, tp := tenancy.TenantFrom(ctx)
	return securitySnapshot{principal: p, principalPresent: pp, tenant: t, tenantPresent: tp}
}
func (s securitySnapshot) matches(ctx context.Context) bool {
	other := captureSecurity(ctx)
	return s.principalPresent == other.principalPresent && s.tenantPresent == other.tenantPresent && s.tenant == other.tenant && s.principal.Equal(other.principal)
}

type factoryView struct {
	mu      sync.Mutex
	live    bool
	busy    bool
	idle    chan struct{}
	allowed map[Key]bool
	path    []Key
	root    bool
}

func (v *factoryView) enter(key Key) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.live {
		return failure("resolve", key, v.path, ErrFactoryScopeExpired, nil)
	}
	if v.busy {
		return failure("resolve", key, v.path, ErrConcurrentFactoryUse, nil)
	}
	if !v.allowed[key] {
		return failure("resolve", key, appendPath(v.path, key), ErrUndeclaredDependency, nil)
	}
	v.busy = true
	v.idle = make(chan struct{})
	return nil
}
func (v *factoryView) leave() { v.mu.Lock(); v.busy = false; close(v.idle); v.mu.Unlock() }
func (v *factoryView) expire() {
	v.mu.Lock()
	v.live = false
	busy, idle := v.busy, v.idle
	v.mu.Unlock()
	// Join a child call admitted while live, even if the factory misused a retained view.
	if busy {
		<-idle
	}
}

// CheckContext rejects principal/tenant replacement (including metadata presence).
// Correlation and receipt may change. Expired factory views are rejected.
func (s *Scope) CheckContext(ctx context.Context) error {
	if s == nil || s.state == nil || ctx == nil {
		return failure("check context", Key{}, nil, ErrInvalidScope, nil)
	}
	if s.view != nil {
		s.view.mu.Lock()
		live := s.view.live
		s.view.mu.Unlock()
		if !live {
			return failure("check context", Key{}, nil, ErrFactoryScopeExpired, nil)
		}
		if s.view.root {
			return nil
		}
	}
	if !s.state.security.matches(ctx) {
		return failure("check context", Key{}, nil, ErrContextMismatch, nil)
	}
	return nil
}

// Close joins admitted resolutions and releases owned values in reverse creation order.
// It unregisters the scope, never commits command effects, and is safe to repeat.
// Factory views cannot close their parent scope. Context deadlines are cooperative.
func (s *Scope) Close(ctx context.Context) error {
	if s == nil || s.state == nil || s.view != nil || ctx == nil {
		return failure("close scope", Key{}, nil, ErrInvalidScope, nil)
	}
	return s.state.close(ctx, false)
}
func (s *scopeState) close(ctx context.Context, force bool) error {
	return s.owner.close(ctx, force, func(ctx context.Context) error { err := s.owner.cleanup(ctx); s.provider.unregister(s); return err })
}
