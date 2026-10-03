// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"net/http"

	"github.com/cratis/arc.go/correlation"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/internal/websockettransport"
	"github.com/cratis/arc.go/queries"
)

func (a *Application) directWebSocket(w http.ResponseWriter, r *http.Request, name queries.FullyQualifiedQueryName, request queries.Request) {
	if !a.hubOriginAllowed(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	p, ok := a.queries.(queries.ObservablePipeline)
	if !ok {
		a.publish(w, r, http.StatusInternalServerError, queries.FromError[any](correlation.FromContext(r.Context()), queries.ErrObservableCapability))
		return
	}
	c, err := a.newWebSocketHub(r)
	if err != nil {
		w.WriteHeader(hubControlStatus(err))
		return
	}
	defer c.cancel()
	var socket *websockettransport.Connection
	c.writer, err = a.websocketWriter(c, func(ctx context.Context, body []byte) error { return socket.Write(ctx, body) }, false)
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
	o, admission, err := p.Open(c.ctx, name, request)
	if o == nil {
		close(readerDone)
		status := admission.StatusCode()
		if errors.Is(err, queries.ErrObservationCapacity) {
			status = http.StatusTooManyRequests
		}
		if errors.Is(err, queries.ErrObservationsStopping) {
			status = http.StatusServiceUnavailable
		}
		a.publish(w, r, status, admission)
		return
	}
	defer func() {
		ctx, cancel, err := boundary.CleanupContext(c.ctx, a.options.Observable.CloseGrace)
		if err == nil {
			defer cancel()
			err = o.Close(ctx)
		}
		if err != nil {
			a.hostFailure(c.ctx, "observable WebSocket source cleanup failed", err)
		}
	}()
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
	go func() {
		defer close(readerDone)
		defer c.cancel()
		// Direct clients need only application Ping/Pong, never Connected or
		// subscription controls. Protocol failures close this connection alone.
		_ = a.readWebSocket(c, socket, false)
	}()
	err = o.Run(c.ctx, queries.ObservationOptions{TransferMode: queries.Full, SkipEnumerableNull: true}, func(result queries.Result[any]) error {
		body, encodeErr := encode(c.ctx, struct {
			Type string              `json:"type"`
			Data queries.Result[any] `json:"data"`
		}{"Data", result})
		if encodeErr != nil || int64(len(body)) > a.options.HTTP.MaxResponseBytes {
			if encodeErr == nil {
				encodeErr = streaming.ErrFrameCapacity
			}
			failure, _ := publicationFailure(result)
			body, fallbackErr := encode(c.ctx, struct {
				Type string `json:"type"`
				Data any    `json:"data"`
			}{"Data", failure})
			if fallbackErr == nil && int64(len(body)) <= a.options.HTTP.MaxResponseBytes {
				fallbackErr = c.writer.Deliver(c.ctx, body, nil)
			}
			return errors.Join(encodeErr, fallbackErr)
		}
		return c.writer.Deliver(c.ctx, body, nil)
	})
	if err != nil && c.ctx.Err() == nil {
		a.hostFailure(c.ctx, "observable WebSocket delivery failed", err)
	}
	// Transfer exclusive write ownership to the orderly-close handshake only
	// after the application writer has actually joined. A timed-out writer is
	// canceled/aborted instead, never overlapped with a second application write.
	cleanup, cancel, cleanupErr := boundary.CleanupContext(c.ctx, a.options.Observable.CloseGrace)
	if cleanupErr == nil {
		defer cancel()
		cleanupErr = c.writer.Close(cleanup)
		select {
		case <-c.writer.Done():
			cleanupErr = errors.Join(cleanupErr, socket.Close(cleanup))
		default:
		}
	}
	if cleanupErr != nil && c.ctx.Err() == nil {
		a.hostFailure(c.ctx, "observable WebSocket close failed", cleanupErr)
	}
	c.cancel()
}
