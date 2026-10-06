// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package clientfixture_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cratis/arc.go/ContractTests/internal/observables/clientfixture"
)

const browserHubPath = "/.cratis/queries/sse"

func TestBrowserModeRequiresAssets(t *testing.T) {
	if fixture, err := clientfixture.NewBrowser(nil); fixture != nil || err == nil {
		t.Fatal("missing browser assets must not silently select the Node owner")
	}
}

func TestBrowserModeServesSameHostAssetsAndGeneratedQueries(t *testing.T) {
	fixture := newBrowserFixture(t)
	origin, _ := serveBrowserFixture(t, fixture)
	client := browserFixtureClient(t, false)
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/fixture/browser/", "<main>fixture</main>"},
		{"/fixture/browser/app.js", "export const fixture = true;"},
	} {
		response := browserFixtureRequest(t, client, http.MethodGet, origin+tc.path, "", "")
		body, err := io.ReadAll(response.Body)
		closeBrowserBody(t, response)
		if err != nil || response.StatusCode != http.StatusOK || string(body) != tc.want {
			t.Fatalf("asset %s: status=%d body=%q error=%v", tc.path, response.StatusCode, body, err)
		}
	}
	if got := fixture.Signals(); got != (clientfixture.Signals{}) {
		t.Fatalf("asset reads activated a source: %+v", got)
	}
	response := browserFixtureRequest(t, client, http.MethodGet, origin+"/items?group=alpha", "", "")
	var result struct {
		IsSuccess bool                 `json:"isSuccess"`
		Data      []clientfixture.Item `json:"data"`
	}
	err := json.NewDecoder(response.Body).Decode(&result)
	closeBrowserBody(t, response)
	if err != nil || response.StatusCode != http.StatusOK || !result.IsSuccess || len(result.Data) != 2 || result.Data[0].CreatedAt.IsZero() {
		t.Fatalf("generated snapshot missing: status=%d result=%+v error=%v", response.StatusCode, result, err)
	}
}

// This is a native HTTP fixture regression, not a browser/CORS/React DOM witness.
func TestBrowserModeUsesIsolatedCookieOwnersAndJoinsSources(t *testing.T) {
	fixture := newBrowserFixture(t)
	origin, stop := serveBrowserFixture(t, fixture)
	owner := browserFixtureClient(t, true)
	foreign := browserFixtureClient(t, true)
	missing := browserFixtureClient(t, false)
	first := openBrowserFixtureHub(t, owner, origin)
	second := openBrowserFixtureHub(t, foreign, origin)
	cookies := first.response.Cookies()
	otherCookies := second.response.Cookies()
	if len(cookies) != 1 || len(otherCookies) != 1 {
		t.Fatal("browser mode did not issue one ownership cookie per connection")
	}
	cookie := cookies[0]
	if cookie.Name != "cratis-observable-"+first.id || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != browserHubPath || cookie.Secure || cookie.MaxAge <= 0 {
		t.Fatal("unexpected HTTP-loopback ownership cookie attributes")
	}
	if cookie.Value == "" || cookie.Value == first.id || cookie.Value == otherCookies[0].Value || first.id == second.id {
		t.Fatal("ownership evidence is not distinct from connection IDs and other owners")
	}
	assetURL, err := url.Parse(origin + "/fixture/browser/")
	if err != nil {
		t.Fatal(err)
	}
	if len(owner.Jar.Cookies(assetURL)) != 0 {
		t.Fatal("hub ownership cookie escaped its path onto browser assets")
	}
	request := map[string]any{"queryName": fixture.Names["All"], "arguments": map[string]string{"group": "alpha"}, "transferMode": "full"}
	control := map[string]any{"connectionId": first.id, "queryId": "owned", "revision": 1, "request": request}
	for _, c := range []*http.Client{missing, foreign} {
		postBrowserFixtureControl(t, c, origin, "/subscribe", control, origin, http.StatusNotFound)
	}
	for _, badOrigin := range []string{"http://foreign.invalid", "null"} {
		postBrowserFixtureControl(t, owner, origin, "/subscribe", control, badOrigin, http.StatusForbidden)
		response := browserFixtureRequest(t, missing, http.MethodGet, origin+browserHubPath, "", badOrigin)
		closeBrowserBody(t, response)
		if response.StatusCode != http.StatusForbidden || len(response.Cookies()) != 0 || response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("foreign Origin opened a hub or received CORS permission")
		}
	}
	if got := fixture.Signals(); got != (clientfixture.Signals{}) {
		t.Fatalf("rejected controls activated dependencies: %+v", got)
	}
	postBrowserFixtureControl(t, owner, origin, "/subscribe", control, origin, http.StatusOK)
	frame := first.read(t)
	if frame.Type != "QueryResult" || frame.QueryID != "owned" {
		t.Fatalf("missing owned result: type=%q query=%q", frame.Type, frame.QueryID)
	}
	waitBrowserFixture(t, owner, origin, "event=open&count=1")
	before := fixture.Signals()
	for _, c := range []*http.Client{missing, foreign} {
		postBrowserFixtureControl(t, c, origin, "/unsubscribe", control, origin, http.StatusNotFound)
	}
	if got := fixture.Signals(); got != before {
		t.Fatalf("foreign unsubscribe changed lifecycle: before=%+v after=%+v", before, got)
	}
	postBrowserFixtureControl(t, owner, origin, "/unsubscribe", control, origin, http.StatusOK)
	waitBrowserFixture(t, owner, origin, "event=close&count=1")
	waitBrowserFixture(t, owner, origin, "event=dispose&count=1")

	// Leave an accepted source active so host shutdown, not test-client cleanup,
	// must close and dispose it. Both physical SSE connections also remain open.
	control["revision"] = 2
	postBrowserFixtureControl(t, owner, origin, "/subscribe", control, origin, http.StatusOK)
	if frame := first.read(t); frame.Type != "QueryResult" {
		t.Fatalf("replacement source did not deliver: %q", frame.Type)
	}
	waitBrowserFixture(t, owner, origin, "event=open&count=2")
	stop()
	for _, hub := range []*browserFixtureHub{first, second} {
		if hub.scanner.Scan() || hub.scanner.Err() != nil {
			t.Fatalf("hub did not terminate cleanly on joined shutdown: %v", hub.scanner.Err())
		}
	}
	if got := fixture.Signals(); got.Resolve != 2 || got.Factory != 2 || got.Open != 2 || got.Close != 2 || got.Dispose != 2 {
		t.Fatalf("joined shutdown lifecycle: %+v", got)
	}
}

func TestNodeModeRetainsExplicitAnonymousOwner(t *testing.T) {
	fixture, err := clientfixture.NewMode(true)
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := serveBrowserFixture(t, fixture)
	client := browserFixtureClient(t, false)
	hub := openBrowserFixtureHub(t, client, origin)
	if len(hub.response.Cookies()) != 0 {
		t.Fatal("Node mode unexpectedly requires cookie ownership")
	}
	control := map[string]any{"connectionId": hub.id, "queryId": "node", "revision": 1, "request": map[string]any{"queryName": fixture.Names["All"], "arguments": map[string]string{"group": "alpha"}}}
	postBrowserFixtureControl(t, client, origin, "/subscribe", control, origin, http.StatusOK)
	if frame := hub.read(t); frame.Type != "QueryResult" {
		t.Fatalf("Node mode no longer accepts cookieless controls: %q", frame.Type)
	}
}

func newBrowserFixture(t *testing.T) *clientfixture.Fixture {
	t.Helper()
	fixture, err := clientfixture.NewBrowser(fstest.MapFS{
		"index.html": {Data: []byte("<main>fixture</main>")},
		"app.js":     {Data: []byte("export const fixture = true;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func serveBrowserFixture(t *testing.T, fixture *clientfixture.Fixture) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- fixture.App.Serve(ctx, listener) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("fixture Serve: %v", err)
			}
		case <-time.After(7 * time.Second):
			t.Error("fixture Serve did not join")
		}
	})
	t.Cleanup(stop)
	return "http://" + listener.Addr().String(), stop
}

func browserFixtureClient(t *testing.T, cookies bool) *http.Client {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone()}
	if cookies {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		client.Jar = jar
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func browserFixtureRequest(t *testing.T, client *http.Client, method, target, body, origin string) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func closeBrowserBody(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Error(err)
	}
}

type browserFixtureFrame struct {
	Type    string          `json:"type"`
	QueryID string          `json:"queryId"`
	Payload json.RawMessage `json:"payload"`
}

type browserFixtureHub struct {
	response *http.Response
	scanner  *bufio.Scanner
	id       string
}

func openBrowserFixtureHub(t *testing.T, client *http.Client, origin string) *browserFixtureHub {
	t.Helper()
	response := browserFixtureRequest(t, client, http.MethodGet, origin+browserHubPath, "", origin)
	t.Cleanup(func() { closeBrowserBody(t, response) })
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("hub opening status=%d", response.StatusCode)
	}
	hub := &browserFixtureHub{response: response, scanner: bufio.NewScanner(response.Body)}
	frame := hub.read(t)
	if err := json.Unmarshal(frame.Payload, &hub.id); err != nil || frame.Type != "Connected" || hub.id == "" {
		t.Fatalf("missing Connected frame: type=%q error=%v", frame.Type, err)
	}
	return hub
}

func (hub *browserFixtureHub) read(t *testing.T) browserFixtureFrame {
	t.Helper()
	for hub.scanner.Scan() {
		if data, ok := strings.CutPrefix(hub.scanner.Text(), "data: "); ok {
			var frame browserFixtureFrame
			if err := json.Unmarshal([]byte(data), &frame); err != nil {
				t.Fatal(err)
			}
			// Consume the event delimiter too, so the EOF assertion detects any
			// actual trailing frame rather than an unread empty line.
			if !hub.scanner.Scan() || hub.scanner.Text() != "" {
				t.Fatal("missing SSE event delimiter")
			}
			return frame
		}
	}
	t.Fatalf("hub ended before a frame: %v", hub.scanner.Err())
	return browserFixtureFrame{}
}

func postBrowserFixtureControl(t *testing.T, client *http.Client, origin, path string, control map[string]any, requestOrigin string, want int) {
	t.Helper()
	body, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	response := browserFixtureRequest(t, client, http.MethodPost, origin+browserHubPath+path, string(body), requestOrigin)
	defer closeBrowserBody(t, response)
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != want || len(data) != 0 || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("control %s: status=%d want=%d bodyLength=%d error=%v", path, response.StatusCode, want, len(data), err)
	}
}

func waitBrowserFixture(t *testing.T, client *http.Client, origin, condition string) {
	t.Helper()
	response := browserFixtureRequest(t, client, http.MethodGet, origin+"/fixture/wait?"+condition, "", "")
	defer closeBrowserBody(t, response)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("fixture condition %s not reached: status=%d", condition, response.StatusCode)
	}
}
