// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package fixture

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	arc "github.com/cratis/arc.go"
)

func openSSE(t *testing.T, server *httptest.Server) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+LiveQueryPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	})
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("got %d %v", response.StatusCode, response.Header)
	}
	return response
}

func readSSE(t *testing.T, reader *bufio.Reader, title string) {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	blank, err := reader.ReadString('\n')
	if err != nil || blank != "\n" || !strings.HasPrefix(line, "data: ") {
		t.Fatalf("invalid SSE frame %q %q: %v", line, blank, err)
	}
	var result struct {
		IsReady bool `json:"isReady"`
		Data    Task `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsReady || result.Data.Title != title {
		t.Fatalf("got %+v, want ready %q", result, title)
	}
}

func joined(t *testing.T, f *Application) {
	t.Helper()
	select {
	case err := <-f.LiveStopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("producer stopped with %v, want cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("observable producer was not joined")
	}
}

// VerifySSE checks flushed, ordered direct SSE results and disconnect cleanup.
func VerifySSE(t *testing.T, f *Application, server *httptest.Server) {
	t.Helper()
	response := openSSE(t, server)
	reader := bufio.NewReader(response.Body)
	readSSE(t, reader, "first")
	readSSE(t, reader, "second")
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	joined(t, f)
}

// VerifyWebSocket checks the direct upgrade, data frames and disconnect cleanup.
func VerifyWebSocket(t *testing.T, f *Application, server *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+LiveQueryPath, nil)
	if err != nil {
		if response != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		t.Fatal(err)
	}
	defer func() {
		// An orderly Close already released the socket; the fallback also runs
		// on assertion failure to avoid retaining an upgraded connection.
		if err := connection.CloseNow(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d", response.StatusCode)
	}
	for _, title := range []string{"first", "second"} {
		kind, body, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Type string `json:"type"`
			Data struct {
				Data Task `json:"data"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		if kind != websocket.MessageText || message.Type != "Data" || message.Data.Data.Title != title {
			t.Fatalf("got %d %s, want Data %q", kind, body, title)
		}
	}
	if err := connection.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatal(err)
	}
	joined(t, f)
}

// VerifyShutdown checks cancellation and joining with an active mounted stream,
// then proves stopped Arc admission still fails closed through the host.
func VerifyShutdown(t *testing.T, f *Application, server *httptest.Server) {
	t.Helper()
	response := openSSE(t, server)
	reader := bufio.NewReader(response.Body)
	readSSE(t, reader, "first")
	readSSE(t, reader, "second")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.App.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	joined(t, f)
	if tail, err := io.ReadAll(reader); err != nil || len(tail) != 0 {
		t.Fatalf("shutdown SSE tail = %q, %v", tail, err)
	}
	if r := Do(t, server, http.MethodGet, QueryPath, "", nil); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("stopped application: got %d %q", r.Status, r.Body)
	}
	if err := f.App.Shutdown(ctx); err != nil && !errors.Is(err, arc.ErrStopped) {
		t.Fatal(err)
	}
}
