// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"sync"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
)

// Physical connections and retired workers stay retained until actual joining.
// HTTP handlers own their writer goroutine; operation workers own opening/run
// and initial bounded cleanup. Shutdown retries timed-out source cleanup.
type hubConnections struct {
	mu                   sync.Mutex
	connections          map[string]*hubConnection
	budget               *streaming.Budget
	openings, operations int
	draining             bool
}
type hubConnection struct {
	id, peer      string
	anonymous     bool
	connected     chan struct{}
	readerDone    <-chan struct{}
	frameOverhead int64
	websocket     bool
	owner         streaming.ConnectionOwner
	ctx           context.Context
	cancel        context.CancelFunc
	writer        *streaming.Writer
	subscriptions *streaming.Subscriptions
	mu            sync.Mutex
	workers       map[*streaming.Operation]*hubWorker
	openings      int
	joinGate      chan struct{}
}
type hubWorker struct {
	done          chan struct{}
	observation   *queries.Observation
	cleanupJoined <-chan struct{}
}

func (a *Application) initHubs() error {
	budget, err := streaming.NewBudget(a.options.Observable.MaxStreamingBytes)
	if err != nil {
		return err
	}
	a.hubs = hubConnections{connections: map[string]*hubConnection{}, budget: budget}
	return nil
}
func (a *Application) registerHub(c *hubConnection) error {
	h := &a.hubs
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.draining {
		return streaming.ErrDraining
	}
	if len(h.connections) >= a.options.Observable.MaxConnections {
		return streaming.ErrCapacity
	}
	owned := 0
	for _, existing := range h.connections {
		if c.owner.Equal(existing.owner) || c.anonymous && existing.anonymous && c.peer == existing.peer {
			owned++
		}
	}
	if owned >= a.options.Observable.MaxConnectionsPerOwner {
		return streaming.ErrCapacity
	}
	h.connections[c.id] = c
	return nil
}
func (a *Application) resolveHub(id string, owner streaming.ConnectionOwner) *hubConnection {
	a.hubs.mu.Lock()
	defer a.hubs.mu.Unlock()
	c := a.hubs.connections[id]
	if c == nil || c.ctx.Err() != nil || !c.owner.Equal(owner) {
		return nil
	}
	return c
}
func (a *Application) reserveHub(c *hubConnection, control streaming.SSEControl) (*streaming.Operation, *streaming.Operation, *hubWorker, error) {
	// All transport reservations serialize here; stale controls are no-ops even
	// under opening saturation. Never run application code while holding locks.
	h := &a.hubs
	h.mu.Lock()
	defer h.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if h.draining || c.ctx.Err() != nil {
		return nil, nil, nil, streaming.ErrDraining
	}
	if c.subscriptions.StaleSubscribe(control.QueryID, control.Revision) {
		return nil, nil, nil, nil
	}
	o := a.options.Observable
	if h.openings >= o.MaxOpenings || c.openings >= o.MaxOpeningsPerConnection || h.operations >= o.MaxObservations {
		return nil, nil, nil, streaming.ErrCapacity
	}
	operation, replaced, err := c.subscriptions.Subscribe(control.QueryID, control.Revision)
	if err != nil || operation == nil {
		return operation, replaced, nil, err
	}
	worker := &hubWorker{done: make(chan struct{})}
	c.workers[operation] = worker
	h.openings++
	h.operations++
	c.openings++
	return operation, replaced, worker, nil
}
func (a *Application) hubOpeningFinished(c *hubConnection) {
	a.hubs.mu.Lock()
	c.mu.Lock()
	a.hubs.openings--
	c.openings--
	c.mu.Unlock()
	a.hubs.mu.Unlock()
}
func (a *Application) hubJoined(c *hubConnection, operation *streaming.Operation) {
	a.hubs.mu.Lock()
	c.mu.Lock()
	if _, exists := c.workers[operation]; exists {
		delete(c.workers, operation)
		a.hubs.operations--
		c.subscriptions.Joined(operation)
	}
	c.mu.Unlock()
	a.hubs.mu.Unlock()
}
func joinedHubCleanup(err error) bool {
	return !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)
}
func (a *Application) closeHub(ctx context.Context, c *hubConnection) error {
	c.cancel()
	c.subscriptions.Drain()
	select {
	case c.joinGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.joinGate }()
	err := c.writer.Close(ctx)
	select {
	case <-c.writer.Done():
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
	if c.readerDone != nil {
		select {
		case <-c.readerDone:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
	c.mu.Lock()
	workers := make(map[*streaming.Operation]*hubWorker, len(c.workers))
	for operation, worker := range c.workers {
		workers[operation] = worker
	}
	c.mu.Unlock()
	for operation, worker := range workers {
		select {
		case <-worker.done:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
		if worker.observation != nil {
			cleanupErr := worker.observation.Close(ctx)
			err = errors.Join(err, cleanupErr)
			if !joinedHubCleanup(cleanupErr) {
				return err
			}
		}
		if worker.cleanupJoined != nil {
			select {
			case <-worker.cleanupJoined:
			case <-ctx.Done():
				return errors.Join(err, ctx.Err())
			}
		}
		a.hubJoined(c, operation)
	}
	a.hubs.mu.Lock()
	delete(a.hubs.connections, c.id)
	a.hubs.mu.Unlock()
	return err
}
func (a *Application) drainHubs() {
	a.hubs.mu.Lock()
	a.hubs.draining = true
	for _, c := range a.hubs.connections {
		c.cancel()
		c.subscriptions.Drain()
	}
	a.hubs.mu.Unlock()
}
func (a *Application) closeHubs(ctx context.Context) error {
	a.hubs.mu.Lock()
	a.hubs.draining = true
	connections := make([]*hubConnection, 0, len(a.hubs.connections))
	for _, c := range a.hubs.connections {
		connections = append(connections, c)
	}
	a.hubs.mu.Unlock()
	for _, c := range connections {
		c.cancel()
		c.subscriptions.Drain()
	}
	var err error
	for _, c := range connections {
		err = errors.Join(err, a.closeHub(ctx, c))
	}
	return err
}

// A subscription receives only connection cancellation and explicitly copied
// verified metadata. It cannot borrow a POST's scope, receipt capability, body,
// headers or arbitrary request context values after the response returns.
type hubLifetimeContext struct{ context.Context }

func (hubLifetimeContext) Value(any) any { return nil }
func hubSubscriptionContext(ctx context.Context, request context.Context) context.Context {
	ctx = hubLifetimeContext{ctx}
	if principal, present := identity.PrincipalFrom(request); present {
		ctx = identity.WithPrincipal(ctx, principal)
	}
	if tenant, present := tenancy.TenantFrom(request); present {
		ctx = tenancy.WithTenant(ctx, tenant)
	}
	return correlation.WithID(ctx, correlation.FromContext(request))
}
