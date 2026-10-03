// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"sync"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/queries"
)

// Lifecycle owns explicit startup work. Hooks must honor context and join all
// work they launch. Stop includes a failing Start hook and runs in reverse order.
// Borrowed clients are not closed unless explicitly registered by their owner.
type Lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}
type lifecycleEntry struct {
	name        string
	participant Lifecycle
}
type appState uint8

const (
	stateBuilt appState = iota
	stateStarting
	stateRunning
	stateStopping
	stateStopped
	stateFailed
)

type lifetime struct {
	mu           sync.Mutex
	state        appState
	startDone    chan struct{}
	startErr     error
	entered      []lifecycleEntry
	hooks        []lifecycleEntry
	active       map[uint64]context.CancelFunc
	next         uint64
	idle         chan struct{}
	shutdownDone chan struct{}
	stopErr      error
}

// AddLifecycle registers a named borrowed hook before Build. Names are unique.
func (b *Builder) AddLifecycle(name string, participant Lifecycle) error {
	if b.attempted {
		return ErrFrozen
	}
	if !providerName(name) || nilValue(participant) {
		return ErrInvalidOptions
	}
	for _, hook := range b.hooks {
		if hook.name == name {
			return ErrInvalidOptions
		}
	}
	b.hooks = append(b.hooks, lifecycleEntry{name, participant})
	return nil
}
func (a *Application) initLifetime(hooks []lifecycleEntry) {
	idle := make(chan struct{})
	close(idle)
	a.life = lifetime{hooks: append([]lifecycleEntry(nil), hooks...), active: make(map[uint64]context.CancelFunc), idle: idle}
}

// Start joins a single startup attempt. ctx controls startup only, not application
// or request lifetime after success. Restart after stop or failure is unsupported.
func (a *Application) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	l := &a.life
	l.mu.Lock()
	switch l.state {
	case stateRunning:
		l.mu.Unlock()
		return nil
	case stateStarting:
		done := l.startDone
		l.mu.Unlock()
		select {
		case <-done:
			return a.startResult()
		case <-ctx.Done():
			return ctx.Err()
		}
	case stateStopping, stateStopped:
		l.mu.Unlock()
		return ErrStopped
	case stateFailed:
		err := l.startErr
		l.mu.Unlock()
		return err
	}
	l.state = stateStarting
	l.startDone = make(chan struct{})
	l.mu.Unlock()
	var entered []lifecycleEntry
	var err error
	for _, hook := range l.hooks {
		entered = append(entered, hook)
		err = boundary.Call(ctx, hook.participant.Start)
		if err != nil {
			break
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cleanup, cancel, cleanupErr := boundary.CleanupContext(ctx, a.options.CleanupTimeout)
		if cleanupErr == nil {
			for i := len(entered) - 1; i >= 0; i-- {
				cleanupErr = errors.Join(cleanupErr, boundary.Call(cleanup, entered[i].participant.Stop))
			}
			cancel()
		}
		err = errors.Join(err, cleanupErr)
	}
	l.mu.Lock()
	l.startErr = err
	if err == nil {
		l.entered = entered
		l.state = stateRunning
	} else {
		l.state = stateFailed
	}
	close(l.startDone)
	l.mu.Unlock()
	if err == nil && a.discovery == discoveryAnonymous && a.options.Environment != "Development" && a.options.Logger != nil {
		a.options.Logger.WarnContext(ctx, "Anonymous Arc discovery enabled outside Development")
	}
	return err
}
func (a *Application) startResult() error {
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	return a.life.startErr
}
func (a *Application) admit(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrInvalidOptions
	}
	l := &a.life
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != stateRunning {
		err := ErrNotStarted
		switch l.state {
		case stateStopping:
			err = ErrShuttingDown
		case stateStopped, stateFailed:
			err = ErrStopped
		}
		return ctx, nil, err
	}
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if len(l.active) == 0 {
		l.idle = make(chan struct{})
	}
	l.next++
	key := l.next
	work, cancel := context.WithCancel(ctx)
	l.active[key] = cancel
	release := func() {
		cancel()
		l.mu.Lock()
		delete(l.active, key)
		if len(l.active) == 0 {
			close(l.idle)
		}
		l.mu.Unlock()
	}
	return work, release, nil
}

// Shutdown atomically closes admission, cancels and joins observations (including
// failed opening cleanup), drains ordinary admitted work, and then stops hooks.
// Deadline expiration cancels active work but does not prove callbacks terminated;
// the application remains Stopping and a subsequent call can continue the join.
// Concurrent callers have independent wait budgets. No cleanup is detached.
func (a *Application) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidOptions
	}
	l := &a.life
	for {
		l.mu.Lock()
		if l.state == stateStarting {
			done := l.startDone
			l.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if l.state == stateStopped || l.state == stateFailed {
			err := l.stopErr
			if l.state == stateFailed {
				err = l.startErr
			}
			l.mu.Unlock()
			return err
		}
		if l.shutdownDone != nil {
			done := l.shutdownDone
			l.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		l.state = stateStopping
		l.shutdownDone = make(chan struct{})
		idle := l.idle
		l.mu.Unlock()
		a.drainHubs()
		var err error
		if observations, ok := a.queries.(queries.ObservablePipeline); ok {
			err = observations.CloseObservations(ctx)
		}
		err = errors.Join(err, a.closeHubs(ctx))
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			// Cancellation is not a join. Retain transports, scopes and hooks for
			// a later shutdown attempt, including failed-opening cleanup.
			l.mu.Lock()
			for _, cancel := range l.active {
				cancel()
			}
			close(l.shutdownDone)
			l.shutdownDone = nil
			l.mu.Unlock()
			return errors.Join(err, ctx.Err())
		}
		err = errors.Join(err, a.shutdownServer(ctx))
		select {
		case <-idle:
		case <-ctx.Done():
			l.mu.Lock()
			for _, cancel := range l.active {
				cancel()
			}
			close(l.shutdownDone)
			l.shutdownDone = nil
			l.mu.Unlock()
			return errors.Join(err, ctx.Err())
		}
		// Work is joined before disposal. A budget expired at this point leaves hooks
		// untouched so a later caller can continue safely.
		if ctx.Err() != nil {
			l.mu.Lock()
			close(l.shutdownDone)
			l.shutdownDone = nil
			l.mu.Unlock()
			return errors.Join(err, ctx.Err())
		}
		for len(l.entered) > 0 {
			if ctx.Err() != nil {
				l.mu.Lock()
				l.stopErr = errors.Join(l.stopErr, err)
				close(l.shutdownDone)
				l.shutdownDone = nil
				l.mu.Unlock()
				return errors.Join(err, ctx.Err())
			}
			i := len(l.entered) - 1
			err = errors.Join(err, boundary.Call(ctx, l.entered[i].participant.Stop))
			l.entered = l.entered[:i]
		}
		err = errors.Join(l.stopErr, err)
		l.mu.Lock()
		l.stopErr = err
		l.state = stateStopped
		close(l.shutdownDone)
		l.shutdownDone = nil
		l.mu.Unlock()
		return err
	}
}
