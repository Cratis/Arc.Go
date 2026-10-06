// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package websockettransport isolates the vetted WebSocket implementation from
// the query pipeline and observable source API.
package websockettransport

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// ErrMessageType rejects binary application messages; Arc speaks JSON text.
var ErrMessageType = errors.New("websocket application message must be text")

// Connection owns an upgraded socket. Its caller owns one application reader
// and one application writer, cancellation, and joining those workers.
type Connection struct {
	socket  *websocket.Conn
	network net.Conn
}

type captureWriter struct {
	http.ResponseWriter
	network net.Conn
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.network = conn
	}
	return conn, buffer, err
}

// IsUpgrade selects only a valid RFC handshake shape, never HEAD or a query
// merely containing an Upgrade header. Accept validates the handshake again.
func IsUpgrade(r *http.Request) bool {
	if r.Method != "GET" || !r.ProtoAtLeast(1, 1) || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	contains := func(name, want string) bool {
		for _, value := range r.Header.Values(name) {
			for _, token := range strings.Split(value, ",") {
				if strings.EqualFold(strings.TrimSpace(token), want) {
					return true
				}
			}
		}
		return false
	}
	keys := r.Header.Values("Sec-WebSocket-Key")
	if len(keys) != 1 {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0]))
	return err == nil && len(key) == 16 && contains("Connection", "Upgrade") && contains("Upgrade", "websocket")
}

// Accept upgrades a request after the host has validated its exact origin
// policy. Compression is disabled. The library validates the RFC handshake and
// handles fragmentation/control frames. It writes HTTP failures itself.
func Accept(w http.ResponseWriter, r *http.Request, maxMessageBytes int64) (*Connection, error) {
	captured := &captureWriter{ResponseWriter: w}
	c, err := websocket.Accept(captured, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(maxMessageBytes)
	return &Connection{socket: c, network: captured.network}, nil
}

// Read returns a complete bounded text message, or io.EOF for orderly peer close.
// Cancellation interrupts the operation and closes the underlying connection.
func (c *Connection) Read(ctx context.Context) ([]byte, error) {
	kind, body, err := c.socket.Read(ctx)
	if code := websocket.CloseStatus(err); code == websocket.StatusNormalClosure || code == websocket.StatusGoingAway {
		return nil, io.EOF
	}
	if err != nil {
		return nil, err
	}
	if kind != websocket.MessageText {
		return nil, ErrMessageType
	}
	return body, nil
}

// Write acknowledges only a completed local text-message write. The caller
// supplies a bounded write context; no compression or retry is performed.
func (c *Connection) Write(ctx context.Context, body []byte) error {
	return c.socket.Write(ctx, websocket.MessageText, body)
}

// Close performs an orderly close after application writes have joined. The
// supplied cleanup context bounds the library's close handshake by closing the
// captured network connection. The cancellation callback is explicitly joined.
func (c *Connection) Close(ctx context.Context) error {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = c.network.Close(); close(done) })
	err := c.socket.Close(websocket.StatusNormalClosure, "")
	if !stop() {
		<-done
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Abort closes the socket immediately and joins library-owned workers. The host
// still must join its application reader/writer and subscription workers.
func (c *Connection) Abort() error { return c.socket.CloseNow() }
