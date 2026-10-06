// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

const sseHub = "/.cratis/queries/sse"

type hubTestClient struct {
	client   *http.Client
	server   *httptest.Server
	response *http.Response
	reader   *bufio.Reader
	id       string
}

func openTestHub(t *testing.T, server *httptest.Server, headers http.Header) *hubTestClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *server.Client()
	client.Jar = jar
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+sseHub, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header = headers.Clone()
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	})
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	message := readSSEResult(t, reader)
	id, ok := message["payload"].(string)
	if !ok || id == "" || message["type"] != "Connected" || message["supportsSubscriptionRevisions"] != true || message["keepAliveIntervalMs"] != float64(30000) {
		t.Fatal(message)
	}
	return &hubTestClient{client: &client, server: server, response: response, reader: reader, id: id}
}
func postHub(t *testing.T, client *http.Client, server *httptest.Server, path, body string, headers http.Header, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", server.URL+sseHub+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = http.Header{}
	}
	r.Header.Set("Content-Type", "application/json")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != want || len(data) != 0 {
		t.Fatalf("status/body = %d/%q, want %d/empty: %v", response.StatusCode, data, want, err)
	}
}
func (c *hubTestClient) control(t *testing.T, path, queryID string, revision int, request any, headers http.Header, want int) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"connectionId": c.id, "queryId": queryID, "revision": revision, "request": request})
	if err != nil {
		t.Fatal(err)
	}
	postHub(t, c.client, c.server, path, string(body), headers, want)
}
func registerHubState(t *testing.T, b *arc.Builder, state observable.Source[builderModel], calls *atomic.Int32) {
	t.Helper()
	if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[builderModel], error) {
		if calls != nil {
			calls.Add(1)
		}
		return state, nil
	}), queries.WithPath[queries.NoArguments]("/observe")); err != nil {
		t.Fatal(err)
	}
}
func hubQueryRequest(a *arc.Application) map[string]any {
	return map[string]any{"queryName": a.Catalog().Queries[0].Identity(), "transferMode": "full"}
}

func TestSSEHubConnectedControlsAndEqualRevisionUnsubscribe(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[http2], func(t *testing.T) {
			b, err := arc.NewBuilder(arc.Options{})
			if err != nil {
				t.Fatal(err)
			}
			state, err := observable.NewState(builderModel{Name: "first"}, observable.SubjectOptions[builderModel]{})
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			registerHubState(t, b, state, &calls)
			a, server := startSSEServer(t, b, http2)
			c := openTestHub(t, server, nil)
			if c.response.Header.Get("Content-Length") != "" || c.response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || http2 && c.response.Header.Get("Connection") != "" {
				t.Fatal(c.response.Header)
			}
			cookies := c.response.Cookies()
			if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != sseHub || cookies[0].Secure != http2 || cookies[0].Value == c.id || cookies[0].MaxAge <= 0 {
				t.Fatal(cookies)
			}
			request := hubQueryRequest(a)
			c.control(t, "/subscribe", "q1", 1, request, nil, 200)
			message := readSSEResult(t, c.reader)
			if message["type"] != "QueryResult" || message["queryId"] != "q1" || message["revision"] != float64(1) || message["payload"].(map[string]any)["data"].(map[string]any)["name"] != "first" {
				t.Fatal(message)
			}
			c.control(t, "/subscribe", "q1", 1, request, nil, 200)
			c.control(t, "/unsubscribe", "q1", 1, nil, nil, 200)
			c.control(t, "/subscribe", "q1", 1, request, nil, 200)
			if calls.Load() != 1 {
				t.Fatal("stale control activated", calls.Load())
			}
			c.control(t, "/subscribe", "q1", 2, request, nil, 200)
			message = readSSEResult(t, c.reader)
			if message["revision"] != float64(2) || calls.Load() != 2 {
				t.Fatal(message, calls.Load())
			}
			// A separate physical connection has distinct anonymous evidence, even
			// when first opened in parallel with an empty cookie jar.
			other := openTestHub(t, server, nil)
			body := `{"connectionId":"` + c.id + `","queryId":"q1","revision":2}`
			postHub(t, other.client, server, "/unsubscribe", body, nil, 404)
			postHub(t, server.Client(), server, "/unsubscribe", body, nil, 404)
			c.control(t, "/unsubscribe", "q1", 2, nil, nil, 200)
		})
	}
}
func TestSSEHubOwnershipIncludesVerifiedSubjectAuthStateAndTenant(t *testing.T) {
	auth := authentication.HandlerFunc(func(_ context.Context, r *http.Request) (authentication.Result, error) {
		if r.Header.Get("Test-User") == "" {
			return authentication.Anonymous(), nil
		}
		return authentication.Authenticated(identity.NewPrincipal(identity.PrincipalData{ID: r.Header.Get("Test-User"), AuthenticationType: "verified"}))
	})
	b, err := arc.NewBuilder(arc.Options{Authentication: []authentication.Handler{auth}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	a, server := startSSEServer(t, b, false)
	headers := http.Header{"Test-User": {"alice"}, "X-Cratis-Tenant-Id": {"tenant-a"}}
	c := openTestHub(t, server, headers)
	if len(c.response.Cookies()) != 0 {
		t.Fatal("authenticated owner needs no evidence cookie")
	}
	for _, h := range []http.Header{nil, {"Test-User": {"bob"}, "X-Cratis-Tenant-Id": {"tenant-a"}}, {"Test-User": {"alice"}, "X-Cratis-Tenant-Id": {"tenant-b"}}} {
		c.control(t, "/subscribe", "q", 1, hubQueryRequest(a), h, 404)
	}
	c.control(t, "/subscribe", "q", 1, hubQueryRequest(a), headers, 200)
}
func TestSSEHubRejectsOriginsMalformedControlsAndOversizeWithoutBodies(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{HTTP: arc.HTTPOptions{MaxBodyBytes: 512}, Observable: arc.ObservableOptions{AllowedOrigins: []string{"https://allowed.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	a, server := startSSEServer(t, b, false)
	c := openTestHub(t, server, nil)
	for _, origin := range []string{"null", "https://evil.example", server.URL + "/path", ""} {
		c.control(t, "/subscribe", "q", 1, hubQueryRequest(a), http.Header{"Origin": {origin}}, 403)
	}
	c.control(t, "/subscribe", "q", 1, hubQueryRequest(a), http.Header{"Origin": {server.URL}}, 200)
	c.control(t, "/subscribe", "q2", 1, hubQueryRequest(a), http.Header{"Origin": {"https://allowed.example"}}, 200)
	for _, body := range []string{`{`, `null`, `{}`, `{"connectionId":"` + c.id + `","queryId":"q","revision":1e0}`, `{"connectionId":"` + c.id + `","queryId":"q","revision":1,"Revision":null}`} {
		postHub(t, c.client, server, "/unsubscribe", body, nil, 400)
	}
	postHub(t, c.client, server, "/subscribe", strings.Repeat("x", 513), nil, 413)
	// HEAD probes never allocate a connection or ownership cookie.
	r, err := http.NewRequestWithContext(t.Context(), "HEAD", server.URL+sseHub, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(response.Cookies()) != 0 {
		t.Fatal(response.StatusCode, response.Cookies())
	}
}
func TestSSEHubCreationDenialIs401AndDoesNotKillOtherSubscription(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: "allowed"}, observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	var deniedCalls atomic.Int32
	if err := queries.RegisterObservable[builderModel](b, "Denied", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[builderModel], error) {
		deniedCalls.Add(1)
		return state, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{})); err != nil {
		t.Fatal(err)
	}
	a, server := startSSEServer(t, b, false)
	c := openTestHub(t, server, nil)
	var allowed, denied string
	for _, q := range a.Catalog().Queries {
		if q.Name == "Denied" {
			denied = q.Identity()
		} else {
			allowed = q.Identity()
		}
	}
	c.control(t, "/subscribe", "deny", 1, map[string]any{"queryName": denied, "transferMode": "full"}, nil, 401)
	message := readSSEResult(t, c.reader)
	if message["type"] != "Unauthorized" || message["queryId"] != "deny" || message["payload"] != nil || deniedCalls.Load() != 0 {
		t.Fatal(message, deniedCalls.Load())
	}
	c.control(t, "/subscribe", "allow", 1, map[string]any{"queryName": allowed, "transferMode": "full"}, nil, 200)
	if message := readSSEResult(t, c.reader); message["type"] != "QueryResult" || message["queryId"] != "allow" {
		t.Fatal(message)
	}
	c.control(t, "/subscribe", "unknown", 1, map[string]any{"queryName": "not.a.query", "transferMode": "full"}, nil, 200)
	if message := readSSEResult(t, c.reader); message["type"] != "Error" || message["queryId"] != "unknown" {
		t.Fatal(message)
	}
}

func TestSSEHubRespondsNoStorePrivate(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	c := openTestHub(t, server, nil)
	if got := c.response.Header.Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("hub Cache-Control = %q", got)
	}
}
