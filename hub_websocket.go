// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/cratis/arc.go/identity"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/internal/websockettransport"
)

const hubWSPath = "/.cratis/queries/ws"

// Construct and reserve before upgrade, so capacity and owner failures remain
// ordinary HTTP. Construction starts no reader, writer, or source worker.
func (a *Application) newWebSocketHub(r *http.Request) (*hubConnection, error) {
	id, err := opaqueHubID()
	if err != nil {
		return nil, err
	}
	owner, _, err := a.hubOwner(r, id, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.options.Observable.ConnectionLifetime)
	subscriptions, err := streaming.NewSubscriptions(ctx, streaming.SubscriptionOptions{MaxIDs: a.options.Observable.MaxQueryIDs, MaxOperations: a.options.Observable.MaxSubscriptions})
	if err != nil {
		cancel()
		return nil, err
	}
	principal, _ := identity.PrincipalFrom(r.Context())
	return &hubConnection{id: id, peer: actualPeer(r), anonymous: !principal.IsAuthenticated(), owner: owner, ctx: ctx, cancel: cancel, subscriptions: subscriptions, workers: map[*streaming.Operation]*hubWorker{}, joinGate: make(chan struct{}, 1), connected: make(chan struct{}), websocket: true}, nil
}

func (a *Application) websocketWriter(c *hubConnection, transport streaming.WriteFrame, keepAlive bool) (*streaming.Writer, error) {
	write := func(ctx context.Context, body []byte) error {
		bounded, cancel := context.WithTimeout(ctx, a.options.Observable.WriteTimeout)
		defer cancel()
		return transport(bounded, body)
	}
	o := streaming.WriterOptions{MaxJobs: a.options.Observable.MaxOutboundJobs, MaxFrameBytes: a.options.HTTP.MaxResponseBytes, MaxQueuedBytes: a.options.Observable.MaxQueuedBytes, Application: a.hubs.budget, Owners: c.subscriptions}
	if keepAlive {
		o.KeepAliveInterval = a.options.Observable.KeepAliveInterval
		o.KeepAlive = func(ctx context.Context) error {
			select {
			case <-c.connected:
			default:
				return nil
			}
			timestamp := a.options.Clock().UnixMilli()
			body, err := encode(ctx, streaming.Message{Type: "Ping", Timestamp: &timestamp})
			if err != nil {
				return err
			}
			if int64(len(body)) > a.options.HTTP.MaxResponseBytes {
				return streaming.ErrFrameCapacity
			}
			return write(ctx, body)
		}
	}
	return streaming.NewWriter(c.ctx, o, write)
}

func (a *Application) hubWebSocketEndpoint(w http.ResponseWriter, r *http.Request) {
	if !a.hubOriginAllowed(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.Method == "HEAD" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !websockettransport.IsUpgrade(r) {
		w.WriteHeader(http.StatusUpgradeRequired)
		return
	}
	c, err := a.newWebSocketHub(r)
	if err != nil {
		w.WriteHeader(hubControlStatus(err))
		return
	}
	defer c.cancel()
	// All registry-visible fields are initialized before reservation. Shutdown
	// may close an unstarted writer during the HTTP handshake. The late socket
	// binding is used only by Run after acceptance, never by construction/Close.
	var socket *websockettransport.Connection
	c.writer, err = a.websocketWriter(c, func(ctx context.Context, body []byte) error { return socket.Write(ctx, body) }, true)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	readerDone := make(chan struct{})
	c.readerDone = readerDone
	if err := a.registerHub(c); err != nil {
		_ = c.writer.Close(c.ctx)
		w.WriteHeader(hubControlStatus(err))
		return
	}
	defer a.finishWebSocketHub(c)
	socket, err = websockettransport.Accept(w, r, a.options.HTTP.MaxBodyBytes)
	if err != nil {
		close(readerDone)
		return
	}
	defer func() { _ = socket.Abort() }()
	if c.ctx.Err() != nil {
		close(readerDone)
		return
	}
	go func() { _ = c.writer.Run() }()
	// Reader activation follows Connected acknowledgement, even when a legacy
	// client sends controls immediately onopen. Frames buffer in the bounded
	// socket reader, not an unbounded application opening queue.
	if err := a.sendHub(c, nil, streaming.Message{Type: "Connected", Payload: c.id, KeepAliveIntervalMs: a.options.Observable.KeepAliveInterval.Milliseconds(), SupportsSubscriptionRevisions: true}, false); err != nil {
		close(readerDone)
		return
	}
	close(c.connected)
	go func() {
		defer close(readerDone)
		defer c.cancel()
		if err := a.readWebSocket(c, socket, true); c.ctx.Err() == nil && !errors.Is(err, io.EOF) {
			a.hostFailure(c.ctx, "observable WebSocket reader failed", err)
		}
	}()
	select {
	case <-c.ctx.Done():
	case <-c.writer.Done():
	}
}

func (a *Application) finishWebSocketHub(c *hubConnection) {
	cleanup, cancel, err := boundary.CleanupContext(c.ctx, a.options.Observable.CloseGrace)
	if err == nil {
		defer cancel()
		err = a.closeHub(cleanup, c)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		a.hostFailure(c.ctx, "observable WebSocket cleanup failed", err)
	}
	// Framework socket work is bounded and may not outlive ServeHTTP. Retained
	// uncooperative application cleanup stays in the registry for Shutdown.
	<-c.writer.Done()
	if c.readerDone != nil {
		<-c.readerDone
	}
}

func (a *Application) readWebSocket(c *hubConnection, socket *websockettransport.Connection, hub bool) error {
	for {
		body, err := socket.Read(c.ctx)
		if err != nil {
			return err
		}
		control, err := streaming.ParseWSControl(body, hub)
		if err != nil {
			return err
		}
		switch control.Type {
		case "Ping":
			timestamp := control.Timestamp
			if timestamp == nil {
				var now int64
				if err := boundary.Call(c.ctx, func(context.Context) error { now = a.options.Clock().UnixMilli(); return nil }); err != nil {
					return err
				}
				timestamp = &now
			}
			if err := a.sendHub(c, nil, streaming.Message{Type: "Pong", Timestamp: timestamp}, false); err != nil {
				return err
			}
		case "Pong":
		case "Unsubscribe":
			if err := c.subscriptions.Unsubscribe(control.QueryID, control.Revision); err != nil {
				return err
			}
		case "Subscribe":
			operation, replaced, worker, err := a.reserveHub(c, streaming.SSEControl{QueryID: control.QueryID, Revision: control.Revision, Request: control.Request})
			if err != nil {
				if err := a.sendHub(c, nil, streaming.Message{Type: "Error", QueryID: control.QueryID, Revision: control.Revision, Payload: "Unable to admit observable query subscription."}, false); err != nil {
					return err
				}
				continue
			}
			if operation == nil {
				continue
			}
			ctx := hubSubscriptionContext(operation.Context(), c.ctx)
			// The reader never waits for a performer or source startup. The shared
			// reservation counts both opening and retired-but-unjoined workers.
			go a.runHubSubscription(c, operation, replaced, worker, ctx, *control.Request, make(chan int, 1))
		}
	}
}
