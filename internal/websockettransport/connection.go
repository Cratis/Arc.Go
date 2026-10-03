// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package websockettransport isolates the vetted WebSocket implementation from
// the query pipeline and observable source API.
package websockettransport

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/coder/websocket"
)

// ErrMessageType rejects binary application messages; Arc speaks JSON text.
var ErrMessageType = errors.New("websocket application message must be text")

// Connection owns an upgraded socket. Its caller owns one application reader
// and one application writer, cancellation, and joining those workers.
type Connection struct{ socket *websocket.Conn }

// Accept upgrades a request after the host has validated its exact origin
// policy. Compression is disabled. The library validates the RFC handshake and
// handles fragmentation/control frames. It writes HTTP failures itself.
func Accept(w http.ResponseWriter, r *http.Request, maxMessageBytes int64) (*Connection, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(maxMessageBytes)
	return &Connection{socket: c}, nil
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

// Abort closes the socket immediately and joins library-owned workers. The host
// still must join its application reader/writer and subscription workers.
func (c *Connection) Abort() error { return c.socket.CloseNow() }
