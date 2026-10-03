// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

type serverState struct {
	mu      sync.Mutex
	server  *http.Server
	claimed bool
}

// Run listens on address and delegates owned lifecycle to Serve. The executable
// owns signal handling. Arc never installs signals, exits or replaces global logs.
func (a *Application) Run(ctx context.Context, address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return a.Serve(ctx, listener)
}

// Serve takes ownership of listener, starts Arc and owns one http.Server. Context
// cancellation initiates shutdown with a fresh bounded budget. The server worker
// is joined before return; uncooperative callbacks can leave Arc Stopping.
func (a *Application) Serve(ctx context.Context, listener net.Listener) error {
	if ctx == nil || nilValue(listener) {
		return ErrInvalidOptions
	}
	a.server.mu.Lock()
	if a.server.claimed {
		a.server.mu.Unlock()
		return errors.Join(ErrAlreadyServing, listener.Close())
	}
	a.server.claimed = true
	h := a.options.HTTP
	server := &http.Server{Handler: a, ReadHeaderTimeout: h.ReadHeaderTimeout, ReadTimeout: h.ReadTimeout, WriteTimeout: h.WriteTimeout, IdleTimeout: h.IdleTimeout, MaxHeaderBytes: h.MaxHeaderBytes}
	a.server.server = server
	a.server.mu.Unlock()
	if err := a.Start(ctx); err != nil {
		return errors.Join(err, listener.Close())
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	var serveErr error
	var joined bool
	select {
	case serveErr = <-done:
		joined = true
	case <-ctx.Done():
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.ShutdownTimeout)
	shutdownErr := a.Shutdown(cleanup)
	cancel()
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	if !joined {
		serveErr = <-done
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}
func (a *Application) shutdownServer(ctx context.Context) error {
	a.server.mu.Lock()
	server := a.server.server
	a.server.mu.Unlock()
	if server == nil {
		return nil
	}
	err := server.Shutdown(ctx)
	if err != nil {
		return errors.Join(err, server.Close())
	}
	return nil
}
