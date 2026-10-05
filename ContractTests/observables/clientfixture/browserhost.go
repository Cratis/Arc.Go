// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package clientfixture

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
)

// BrowserReport is one host lifecycle report for the real-browser runner.
// Restarted reports a completed server close of one generation; Joined reports
// the final join after the caller's context ended. Signals belong to the
// generation that just stopped and are cumulative within that generation.
type BrowserReport struct {
	Joined     bool    `json:"joined,omitempty"`
	Restarted  int     `json:"restarted,omitempty"`
	Generation int     `json:"generation"`
	Signals    Signals `json:"signals"`
}

// ServeBrowser serves NewBrowser fixtures on listener until ctx ends.
//
// POST /fixture/shutdown is a real server close, not a simulated network fault:
// the current generation's App.Serve is cancelled and joined, every opened source
// must have closed, a new listener is bound to the same loopback address, then
// report receives a Restarted entry and a fresh fixture serves it. Browsers keep
// their own reconnect policy; this host never pushes a reconnect to a client.
//
// When ctx ends, the current generation is joined and report receives a final
// Joined entry. The listener is owned and closed by App.Serve. A generation that
// leaves a source unjoined, a rebind failure, or a report error stops the host
// with an error. report is called synchronously from the calling goroutine.
// The host is a loopback-only, unauthenticated test fixture.
func ServeBrowser(ctx context.Context, listener net.Listener, assets fs.FS, report func(BrowserReport) error) error {
	if listener == nil || report == nil {
		return errors.Join(fmt.Errorf("browser host requires a listener and a report callback"), closeListener(listener))
	}
	address := listener.Addr().String()
	for generation := 1; ; generation++ {
		fixture, err := NewBrowser(assets)
		if err != nil {
			return errors.Join(err, listener.Close())
		}
		restart, err := serveGeneration(ctx, fixture, listener)
		if err != nil {
			return err
		}
		signals := fixture.Signals()
		if signals.Open != signals.Close {
			return fmt.Errorf("browser host generation %d left sources unjoined: %+v", generation, signals)
		}
		if !restart {
			return report(BrowserReport{Joined: true, Generation: generation, Signals: signals})
		}
		listener, err = net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("rebind browser host %s: %w", address, err)
		}
		if err := report(BrowserReport{Restarted: generation, Generation: generation, Signals: signals}); err != nil {
			return errors.Join(err, listener.Close())
		}
	}
}

// serveGeneration joins one App.Serve and reports whether it ended because the
// fixture requested a restart rather than because ctx ended.
func serveGeneration(ctx context.Context, fixture *Fixture, listener net.Listener) (bool, error) {
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	restart := make(chan bool, 1)
	go func() {
		select {
		case <-fixture.Shutdown():
			restart <- true
			cancel()
		case <-serveCtx.Done():
			restart <- false
		}
	}()
	err := fixture.App.Serve(serveCtx, listener)
	cancel()
	restarted := <-restart
	// A restart request racing the caller's cancellation is a final shutdown.
	return restarted && ctx.Err() == nil, err
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	return listener.Close()
}
