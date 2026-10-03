// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observables_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

// These expectations are independent of the server DTOs and encoders. See the
// v1/observable fixture provenance for the pinned C#/JavaScript authority.
type Item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

var initial = []Item{{ID: "a", Title: "old"}, {ID: "b", Title: "gone"}}
var updated = []Item{{ID: "a", Title: "changed"}, {ID: "c", Title: "new"}}

type host struct {
	app    *arc.Application
	server *httptest.Server
	state  *observable.State[[]Item]
	deny   atomic.Bool
	calls  atomic.Int32
	names  map[string]string
}

func newHost(t *testing.T) *host {
	t.Helper()
	h := &host{names: map[string]string{}}
	var err error
	h.state, err = observable.NewState(initial, observable.SubjectOptions[[]Item]{Buffer: 8})
	if err != nil {
		t.Fatal(err)
	}
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"All", "Guarded"} {
		err := queries.RegisterObservable[Item](builder, name, queries.Function(func(context.Context, queries.NoArguments) (observable.Source[[]Item], error) {
			h.calls.Add(1)
			return h.state, nil
		}), queries.WithPath[queries.NoArguments]("/"+strings.ToLower(name)), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Queries().AddEmissionGuard("deny", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
			if h.deny.Load() && strings.HasSuffix(string(c.Name()), ".Guarded") {
				return queries.DenyAndTerminate, nil
			}
			return queries.Allow, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	h.app, err = builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, query := range h.app.Catalog().Queries {
		h.names[query.Name] = query.Identity()
	}
	h.server = httptest.NewServer(h.app)
	// Shutdown precedes listener cleanup even when an assertion fails with live
	// SSE or hijacked WS connections. A deadline is not accepted as a join.
	t.Cleanup(func() {
		h.shutdown(t)
		h.server.Close()
	})
	return h
}

func (h *host) shutdown(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.app.Shutdown(ctx); err != nil {
		t.Errorf("shutdown did not join: %v", err)
	}
}

func (h *host) publish(t *testing.T, items []Item) {
	t.Helper()
	if err := h.state.Publish(t.Context(), items); err != nil {
		t.Fatal(err)
	}
}

type peer struct {
	read    func() (map[string]any, error)
	control func(kind, id string, revision int, request any)
	close   func()
}

func (p *peer) message(t *testing.T) map[string]any {
	t.Helper()
	message, err := p.read()
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func openPeer(t *testing.T, h *host, transport, path string) *peer {
	t.Helper()
	p := &peer{}
	if transport == "ws" {
		ws := openWebSocket(t, h.server.URL+path)
		p.read = ws.read
		p.close = func() { _ = ws.conn.Close() }
		p.control = func(kind, id string, revision int, request any) {
			ws.send(t, map[string]any{"type": kind, "queryId": id, "revision": wireRevision(revision), "payload": request})
		}
	} else {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Jar: jar}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		t.Cleanup(cancel)
		r, err := http.NewRequestWithContext(ctx, "GET", h.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Accept", "text/event-stream")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		p.close = func() { _ = response.Body.Close(); client.CloseIdleConnections() }
		if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || response.Header.Get("Content-Length") != "" {
			p.close()
			t.Fatalf("SSE response: %d %v", response.StatusCode, response.Header)
		}
		reader := bufio.NewReader(response.Body)
		p.read = func() (map[string]any, error) {
			line, err := reader.ReadString('\n')
			if err != nil {
				return nil, err
			}
			if !strings.HasPrefix(line, "data: ") {
				t.Fatalf("SSE framing: %q", line)
			}
			end, err := reader.ReadString('\n')
			if err != nil {
				return nil, err
			}
			if end != "\n" {
				t.Fatalf("SSE frame terminator: %q", end)
			}
			return decodeMessage([]byte(strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")))
		}
		var connectionID string
		p.control = func(kind, id string, revision int, request any) {
			body, err := json.Marshal(map[string]any{"connectionId": connectionID, "queryId": id, "revision": wireRevision(revision), "request": request})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			r, err := http.NewRequestWithContext(ctx, "POST", h.server.URL+path+"/"+strings.ToLower(kind), bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Content-Type", "application/json")
			response, err := client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			data, err := io.ReadAll(io.LimitReader(response.Body, 1024))
			if err != nil || response.StatusCode != 200 || len(data) != 0 {
				t.Fatalf("control status/body: %d/%q: %v", response.StatusCode, data, err)
			}
		}
		if strings.HasPrefix(path, "/.cratis/") {
			connectionID = connected(t, p.message(t))
		}
	}
	t.Cleanup(p.close)
	if transport == "ws" && strings.HasPrefix(path, "/.cratis/") {
		connected(t, p.message(t))
	}
	return p
}

// Zero in the test harness deliberately means the reference nullable legacy
// revision, never a wire zero (which the server must reject).
func wireRevision(revision int) any {
	if revision == 0 {
		return nil
	}
	return revision
}

func decodeMessage(body []byte) (map[string]any, error) {
	var message map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // Never round revision/timestamp values through float64.
	if err := decoder.Decode(&message); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing message content")
	}
	return message, nil
}

func connected(t *testing.T, message map[string]any) string {
	t.Helper()
	id, ok := message["payload"].(string)
	if !ok || id == "" || message["type"] != "Connected" || message["supportsSubscriptionRevisions"] != true || message["keepAliveIntervalMs"] != json.Number("30000") || len(message) != 4 {
		t.Fatalf("Connected: %v", message)
	}
	return id
}

func hubResult(t *testing.T, message map[string]any, id string, revision int) map[string]any {
	t.Helper()
	if message["type"] != "QueryResult" || message["queryId"] != id || message["revision"] != json.Number(strconv.Itoa(revision)) || len(message) != 4 {
		t.Fatalf("hub envelope: %v", message)
	}
	payload, ok := message["payload"].(map[string]any)
	if !ok {
		t.Fatal("missing QueryResult payload", message)
	}
	return payload
}

func assertResult(t *testing.T, result map[string]any, items []Item, changes string) {
	t.Helper()
	for _, key := range []string{"isSuccess", "isReady", "isAuthorized", "isValid"} {
		if result[key] != true {
			t.Fatalf("%s: %v", key, result)
		}
	}
	if result["hasExceptions"] != false || result["exceptionStackTrace"] != "" || !reflect.DeepEqual(result["validationResults"], []any{}) || !reflect.DeepEqual(result["exceptionMessages"], []any{}) {
		t.Fatal("required failure fields", result)
	}
	id, ok := result["correlationId"].(string)
	if !ok {
		t.Fatal("missing correlation", result)
	}
	if _, err := concepts.ParseUUID(id); err != nil {
		t.Fatal("invalid correlation", result)
	}
	paging := map[string]any{"page": json.Number("0"), "size": json.Number("0"), "totalItems": json.Number("0"), "totalPages": json.Number("0")}
	if !reflect.DeepEqual(result["paging"], paging) {
		t.Fatal("paging", result)
	}
	if items == nil {
		if _, exists := result["data"]; exists {
			t.Fatal("delta must omit data", result)
		}
	} else {
		body, err := json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		var want any
		if err := json.Unmarshal(body, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result["data"], want) {
			t.Fatalf("data = %v; want %v", result["data"], want)
		}
	}
	if changes == "" {
		if _, exists := result["changeSet"]; exists {
			t.Fatal("unexpected changes", result)
		}
	} else {
		var want any
		if err := json.Unmarshal([]byte(changes), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result["changeSet"], want) {
			t.Fatalf("changes = %v; want %v", result["changeSet"], want)
		}
	}
}

const firstChanges = `{"added":[{"id":"a","title":"old"},{"id":"b","title":"gone"}],"replaced":[],"removed":[]}`
const nextChanges = `{"added":[{"id":"c","title":"new"}],"replaced":[{"id":"a","title":"changed"}],"removed":[{"id":"b","title":"gone"}]}`

func TestDirectTransportsDeliverFullResultsAndTerminalDenial(t *testing.T) {
	for _, transport := range []string{"sse", "ws"} {
		t.Run(transport, func(t *testing.T) {
			h := newHost(t)
			p := openPeer(t, h, transport, "/guarded")
			read := func() map[string]any {
				message := p.message(t)
				if transport == "ws" {
					if message["type"] != "Data" || len(message) != 2 {
						t.Fatal("direct WS envelope", message)
					}
					return message["data"].(map[string]any)
				}
				return message
			}
			first := read()
			assertResult(t, first, initial, "")
			h.publish(t, updated)
			second := read()
			assertResult(t, second, updated, "")
			if first["correlationId"] != second["correlationId"] {
				t.Fatal("subscription correlation changed")
			}
			h.deny.Store(true)
			h.publish(t, initial)
			terminal := read()
			if terminal["isAuthorized"] != false || terminal["isReady"] != true || terminal["isSuccess"] != false || terminal["data"] != nil || terminal["changeSet"] != nil {
				t.Fatal("terminal denial", terminal)
			}
			if message, err := p.read(); !errors.Is(err, io.EOF) {
				t.Fatalf("data behind denial or no orderly close: %v %v", message, err)
			}
			// Failure belongs to the subscriber, never the shared source.
			h.publish(t, updated)
		})
	}
}

func TestHubsTransferModesRevisionsTombstonesAndReconnect(t *testing.T) {
	for _, transport := range []string{"sse", "ws"} {
		for _, mode := range []string{"full", "delta", "legacy"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				h := newHost(t)
				path := "/.cratis/queries/" + transport
				p := openPeer(t, h, transport, path)
				request := map[string]any{"queryName": h.names["All"], "arguments": map[string]any{}, "transferMode": mode}
				p.control("Subscribe", "q", 1, request)
				first := hubResult(t, p.message(t), "q", 1)
				changes := ""
				if mode == "legacy" {
					changes = firstChanges
				}
				assertResult(t, first, initial, changes)
				h.publish(t, updated)
				second := hubResult(t, p.message(t), "q", 1)
				items := updated
				changes = nextChanges
				if mode == "full" {
					changes = ""
				}
				if mode == "delta" {
					items = nil
				}
				assertResult(t, second, items, changes)
				if first["correlationId"] != second["correlationId"] {
					t.Fatal("subscription correlation changed")
				}
				// Equality cancels. No stale or legacy control can resurrect this ID.
				p.control("Unsubscribe", "q", 1, nil)
				p.control("Subscribe", "q", 1, request)
				p.control("Subscribe", "q", 0, request)
				// A tombstone created before any subscribe also prevents late opening.
				p.control("Unsubscribe", "late", 3, nil)
				p.control("Subscribe", "late", 2, request)
				p.control("Subscribe", "q", 2, request)
				replacement := hubResult(t, p.message(t), "q", 2)
				changes = ""
				if mode == "legacy" {
					changes = `{"added":[{"id":"a","title":"changed"},{"id":"c","title":"new"}],"replaced":[],"removed":[]}`
				}
				assertResult(t, replacement, updated, changes)
				if h.calls.Load() != 2 {
					t.Fatal("stale control activated a performer", h.calls.Load())
				}
				// Reconnect is non-resumable: revision one is legal again and the
				// first delta must be a full current snapshot, not prior changes.
				p.close()
				fresh := openPeer(t, h, transport, path)
				fresh.control("Subscribe", "q", 1, request)
				assertResult(t, hubResult(t, fresh.message(t), "q", 1), updated, changes)
			})
		}
	}
}

func TestHubsUnauthorizedIsTerminalOnlyForItsSubscription(t *testing.T) {
	for _, transport := range []string{"sse", "ws"} {
		t.Run(transport, func(t *testing.T) {
			h := newHost(t)
			p := openPeer(t, h, transport, "/.cratis/queries/"+transport)
			for _, id := range []string{"All", "Guarded"} {
				p.control("Subscribe", id, 1, map[string]any{"queryName": h.names[id], "transferMode": "delta"})
				assertResult(t, hubResult(t, p.message(t), id, 1), initial, "")
			}
			h.deny.Store(true)
			h.publish(t, updated)
			seen := map[string]bool{}
			for range 2 {
				message := p.message(t)
				if message["queryId"] == "Guarded" {
					if message["type"] != "Unauthorized" || message["revision"] != json.Number("1") || len(message) != 3 {
						t.Fatal("unauthorized envelope", message)
					}
					seen["Guarded"] = true
				} else {
					assertResult(t, hubResult(t, message, "All", 1), nil, nextChanges)
					seen["All"] = true
				}
			}
			if len(seen) != 2 {
				t.Fatal("missing terminal or sibling result", seen)
			}
			h.publish(t, updated)
			assertResult(t, hubResult(t, p.message(t), "All", 1), nil, `{"added":[],"replaced":[],"removed":[]}`)
		})
	}
}

func TestShutdownJoinsAllFourActiveTransports(t *testing.T) {
	h := newHost(t)
	var peers []*peer
	for _, transport := range []string{"sse", "ws"} {
		direct := openPeer(t, h, transport, "/all")
		direct.message(t)
		peers = append(peers, direct)
		hub := openPeer(t, h, transport, "/.cratis/queries/"+transport)
		hub.control("Subscribe", "q", 1, map[string]any{"queryName": h.names["All"], "transferMode": "delta"})
		hubResult(t, hub.message(t), "q", 1)
		peers = append(peers, hub)
	}
	h.shutdown(t)
	for _, p := range peers {
		if message, err := p.read(); err == nil {
			t.Fatalf("connection remained live after joined shutdown: %v", message)
		}
	}
	h.shutdown(t) // Repeated shutdown is safe after all workers actually joined.
}
