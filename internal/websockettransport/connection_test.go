// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package websockettransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCompleteTextMessagesAndBinaryRejection(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	joined := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r, 64)
		if err != nil {
			joined <- err
			return
		}
		defer func() { _ = c.Abort() }()
		body, err := c.Read(ctx)
		if err == nil {
			err = c.Write(ctx, body)
		}
		if err == nil {
			_, err = c.Read(ctx)
		}
		joined <- err
	}))
	defer server.Close()
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.CloseNow() }()
	if err := client.Write(ctx, websocket.MessageText, []byte(`{"type":"Ping","timestamp":1}`)); err != nil {
		t.Fatal(err)
	}
	kind, body, err := client.Read(ctx)
	if err != nil || kind != websocket.MessageText || string(body) != `{"type":"Ping","timestamp":1}` {
		t.Fatalf("read = %s, %v", body, err)
	}
	if err := client.Write(ctx, websocket.MessageBinary, []byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-joined:
		if !errors.Is(err, ErrMessageType) {
			t.Fatalf("binary error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
