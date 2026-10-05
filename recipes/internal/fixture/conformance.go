// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package fixture

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Response is a fully read HTTP response.
type Response struct {
	Status int
	Header http.Header
	Body   string
}

// Serve starts a real server for handler, closed before the fixture shuts Arc down.
func Serve(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// Do sends one request through server and reads the whole response.
func Do(t testing.TB, server *httptest.Server, method, path, body string, header http.Header) Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return Response{Status: response.StatusCode, Header: response.Header, Body: string(content)}
}

// VerifyMount proves that a host which mounts f.App behind server preserves
// Arc's command, /validate, GET and QUERY dispatch, Arc-owned empty 404/405
// responses and request cancellation.
func VerifyMount(t *testing.T, f *Application, server *httptest.Server) {
	t.Helper()
	t.Run("command executes", func(t *testing.T) {
		before := f.Handled.Load()
		r := Do(t, server, http.MethodPost, CommandPath, `{"title":"Write recipes"}`, nil)
		if r.Status != http.StatusOK || !strings.Contains(r.Body, `"response":"registered"`) || f.Handled.Load() != before+1 {
			t.Fatalf("got %d %q, handled %d->%d", r.Status, r.Body, before, f.Handled.Load())
		}
	})
	t.Run("validate does not execute", func(t *testing.T) {
		before := f.Handled.Load()
		valid := Do(t, server, http.MethodPost, ValidatePath, `{"title":"Write recipes"}`, nil)
		invalid := Do(t, server, http.MethodPost, ValidatePath, `{}`, nil)
		if valid.Status != http.StatusOK || !strings.Contains(valid.Body, `"isSuccess":true`) {
			t.Fatalf("valid: got %d %q", valid.Status, valid.Body)
		}
		if invalid.Status != http.StatusBadRequest || !strings.Contains(invalid.Body, `"isValid":false`) {
			t.Fatalf("invalid: got %d %q", invalid.Status, invalid.Body)
		}
		if f.Handled.Load() != before {
			t.Fatalf("validate executed the handler")
		}
	})
	t.Run("GET binds the query string", func(t *testing.T) {
		r := Do(t, server, http.MethodGet, QueryPath+"?title=from-get", "", nil)
		if r.Status != http.StatusOK || !strings.Contains(r.Body, `"title":"from-get"`) {
			t.Fatalf("got %d %q", r.Status, r.Body)
		}
	})
	t.Run("HEAD suppresses the body", func(t *testing.T) {
		r := Do(t, server, http.MethodHead, QueryPath+"?title=from-head", "", nil)
		if r.Status != http.StatusOK || r.Body != "" || r.Header.Get("Content-Length") == "" {
			t.Fatalf("got %d %q %v", r.Status, r.Body, r.Header)
		}
	})
	t.Run("QUERY binds the body", func(t *testing.T) {
		r := Do(t, server, "QUERY", QueryPath, `{"arguments":{"title":"from-query"}}`, nil)
		if r.Status != http.StatusOK || !strings.Contains(r.Body, `"title":"from-query"`) {
			t.Fatalf("got %d %q", r.Status, r.Body)
		}
		if got := r.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control %q, want no-store", got)
		}
	})
	t.Run("correlation and QUERY privacy headers survive the host", func(t *testing.T) {
		const id = "12345678-1234-4234-8234-123456789012"
		r := Do(t, server, "QUERY", QueryPath, `{"arguments":{"title":"private"}}`, http.Header{"X-Correlation-ID": {id}})
		if r.Status != http.StatusOK || r.Header.Get("X-Correlation-ID") != id ||
			r.Header.Get("Cache-Control") != "no-store" || !strings.Contains(r.Body, `"correlationId":"`+id+`"`) {
			t.Fatalf("got %d %q %v", r.Status, r.Body, r.Header)
		}
	})
	t.Run("unmapped Arc path is Arc's empty 404", func(t *testing.T) {
		r := Do(t, server, http.MethodGet, UnmappedPath, "", nil)
		if r.Status != http.StatusNotFound || r.Body != "" {
			t.Fatalf("got %d %q", r.Status, r.Body)
		}
	})
	t.Run("wrong method is Arc's empty 405", func(t *testing.T) {
		query := Do(t, server, http.MethodDelete, QueryPath, "", nil)
		if query.Status != http.StatusMethodNotAllowed || query.Body != "" || query.Header.Get("Allow") != "GET, HEAD, QUERY" {
			t.Fatalf("query: got %d %q Allow %q", query.Status, query.Body, query.Header.Get("Allow"))
		}
		command := Do(t, server, http.MethodGet, CommandPath, "", nil)
		if command.Status != http.StatusMethodNotAllowed || command.Body != "" || command.Header.Get("Allow") != "POST" {
			t.Fatalf("command: got %d %q Allow %q", command.Status, command.Body, command.Header.Get("Allow"))
		}
	})
	t.Run("client cancellation reaches the query", func(t *testing.T) {
		VerifyCancellation(t, f, server)
	})
	t.Run("direct SSE flushes data and joins on disconnect", func(t *testing.T) {
		VerifySSE(t, f, server)
	})
	t.Run("direct WebSocket upgrades and joins on disconnect", func(t *testing.T) {
		VerifyWebSocket(t, f, server)
	})
	t.Run("application shutdown joins active observations", func(t *testing.T) {
		VerifyShutdown(t, f, server)
	})
}

// VerifyCancellation proves that cancelling the client request cancels the
// context the Arc query observes through the host.
func VerifyCancellation(t *testing.T, f *Application, server *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+SlowQueryPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if err == nil {
			err = errors.Join(errors.New("slow query completed unexpectedly"), response.Body.Close())
		}
		done <- err
	}()
	select {
	case <-f.SlowStarted:
	case err := <-done:
		t.Fatalf("request ended before the query started: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("slow query did not start")
	}
	cancel()
	select {
	case err := <-f.SlowStopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("query observed %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("query context was not cancelled")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("client returned %v, want context.Canceled", err)
	}
}
