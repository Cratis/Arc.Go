//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const twoInstanceHubPath = "/.cratis/queries/sse"

type twoInstanceNode struct {
	provider *providerFixture
	watcher  *mongodb.Watcher
	app      *arc.Application
	server   *httptest.Server
	opened   atomic.Int32
	closed   atomic.Int32
	stopped  bool
}

type twoInstanceResources struct {
	closed *atomic.Int32
	done   atomic.Bool
}

func (r *twoInstanceResources) Close(context.Context) error {
	if r.done.Swap(true) {
		return errors.New("invocation resources closed more than once")
	}
	r.closed.Add(1)
	return nil
}

func newTwoInstanceNode(t *testing.T, collectionName string) *twoInstanceNode {
	t.Helper()
	// Each application owns a distinct client, watcher, pipeline and hub registry.
	// Only the real MongoDB replica set and collection coordinates are shared.
	node := &twoInstanceNode{provider: liveProvider(t)}
	node.watcher = liveWatcher(t, node.provider, mongodb.WatcherOptions{
		MaxDatabases: 2, MaxSubscribers: 8, MaxSubscribersPerQuery: 8, Buffer: 16,
	})
	binding, err := mongodb.NewCollection[providerTask](node.provider.client, mongodb.CollectionOptions{
		Database: node.provider.base, Name: collectionName, Ownership: mongodb.ApplicationOwned,
		SortFields: []queries.SortField{"id", "name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := mongodb.NewRenderer(binding, mongodb.RendererOptions[providerTask]{
		MaxItems: 8, MaxBSONBytes: 16 << 10,
		RowFilter: func(_ context.Context, q queries.QueryContext) (bson.D, error) {
			return bson.D{{Key: "OwnerID", Value: q.Principal().ID()}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	builder, err := arc.NewBuilder(arc.Options{
		Authentication: []authentication.Handler{authentication.HandlerFunc(func(_ context.Context, request *http.Request) (authentication.Result, error) {
			// Test-only credential mapping, not authentication inferred from routing.
			var subject string
			switch request.Header.Get("Authorization") {
			case "Bearer two-instance-reader":
				subject = "reader"
			case "Bearer two-instance-other":
				subject = "other"
			default:
				return authentication.Anonymous(), nil
			}
			return authentication.Authenticated(identity.NewPrincipal(identity.PrincipalData{ID: subject, AuthenticationType: "two-instance fixture"}))
		})},
		RequireTenant: true,
		Membership: tenancy.MembershipFunc(func(_ context.Context, p identity.Principal, tenant tenancy.ID) (bool, error) {
			return p.IsAuthenticated() && (p.ID() == "reader" || p.ID() == "other") && (tenant == node.provider.a || tenant == node.provider.b), nil
		}),
		OpenResources: func(context.Context) (execution.Resources, error) {
			node.opened.Add(1)
			return &twoInstanceResources{closed: &node.closed}, nil
		},
		HTTP: arc.HTTPOptions{MaxResponseBytes: 16 << 10},
		Observable: arc.ObservableOptions{
			MaxObservations: 8, MaxConnections: 3, MaxConnectionsPerOwner: 2,
			MaxSubscriptions: 2, MaxOpenings: 2, MaxOpeningsPerConnection: 2,
			MaxQueryIDs: 4, MaxOutboundJobs: 8, MaxQueuedBytes: 64 << 10,
			MaxStreamingBytes: 128 << 10, CloseGrace: 5 * time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterRenderer[mongodb.Find[providerTask], []providerTask](builder.Queries(), func(context.Context, *execution.Scope) (queries.Renderer[mongodb.Find[providerTask], []providerTask], error) {
		return renderer, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterObservable[providerTask, queries.NoArguments, mongodb.Find[providerTask]](builder, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[mongodb.Find[providerTask]], error) {
		return mongodb.Observe(node.watcher, binding, mongodb.Find[providerTask]{Filter: activeFilter()})
	}), queries.WithPath[queries.NoArguments]("/observe"), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{}), queries.WithCollectionIdentity[queries.NoArguments](func(row providerTask) (any, error) { return row.ID, nil })); err != nil {
		t.Fatal(err)
	}
	node.app, err = builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := node.app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	node.server = httptest.NewServer(node.app)
	t.Cleanup(func() { node.stop(t) })
	return node
}

func (n *twoInstanceNode) stop(t *testing.T) {
	t.Helper()
	if n.stopped {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	// The application joins observations before the watcher joins its database
	// readers. The borrowed client stays usable until liveProvider's cleanup.
	if err := n.app.Shutdown(ctx); err != nil {
		t.Error("application shutdown", err)
		return
	}
	if err := n.watcher.Close(ctx); err != nil {
		t.Error("watcher shutdown", err)
		return
	}
	n.server.Close()
	n.stopped = true
	if got, want := n.closed.Load(), n.opened.Load(); want == 0 || got != want {
		t.Errorf("joined invocation resources = %d, opened = %d", got, want)
	}
	if err := n.provider.client.Ping(ctx, nil); err != nil {
		t.Error("borrowed client was disconnected", err)
	}
}

func twoInstanceRouter(t *testing.T, first, second *twoInstanceNode) *httptest.Server {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	proxies := make(map[string]*httputil.ReverseProxy, 2)
	for name, node := range map[string]*twoInstanceNode{"first": first, "second": second} {
		target, err := url.Parse(node.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		proxies[name] = &httputil.ReverseProxy{Transport: transport, Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Header.Del("X-Test-Instance")
		}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy := proxies[r.Header.Get("X-Test-Instance")]
		if proxy == nil {
			http.Error(w, "fixture requires an explicit target", http.StatusBadGateway)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return server
}

func twoInstanceHeaders(instance, subject string, tenant tenancy.ID) http.Header {
	headers := http.Header{}
	headers.Set("X-Test-Instance", instance)
	headers.Set("X-Cratis-Tenant-Id", tenant.String())
	if subject != "" {
		headers.Set("Authorization", "Bearer two-instance-"+subject)
	}
	return headers
}

type twoInstanceStream struct {
	reader *bufio.Scanner
	id     string
}

func openTwoInstanceStream(t *testing.T, router *httptest.Server, path string, headers http.Header) *twoInstanceStream {
	t.Helper()
	// One bounded request lifetime, not a silence interval proving non-delivery.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, router.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header = headers.Clone()
	request.Header.Set("Accept", "text/event-stream")
	response, err := router.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	})
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatal("SSE admission", response.StatusCode, response.Header)
	}
	reader := bufio.NewScanner(response.Body)
	reader.Buffer(make([]byte, 4096), 32<<10)
	stream := &twoInstanceStream{reader: reader}
	if path == twoInstanceHubPath {
		message := stream.message(t)
		if message.Type != "Connected" || !message.SupportsSubscriptionRevisions || json.Unmarshal(message.Payload, &stream.id) != nil || stream.id == "" {
			t.Fatal("hub connection handshake", message)
		}
	}
	return stream
}

type twoInstanceMessage struct {
	Type                          string          `json:"type"`
	QueryID                       string          `json:"queryId"`
	Revision                      int             `json:"revision"`
	SupportsSubscriptionRevisions bool            `json:"supportsSubscriptionRevisions"`
	Payload                       json.RawMessage `json:"payload"`
}

type twoInstanceResult struct {
	Success    bool              `json:"isSuccess"`
	Ready      bool              `json:"isReady"`
	Authorized bool              `json:"isAuthorized"`
	Data       []providerTask    `json:"data"`
	ChangeSet  *twoInstanceDelta `json:"changeSet"`
}

type twoInstanceDelta struct {
	Added    []providerTask `json:"added"`
	Replaced []providerTask `json:"replaced"`
	Removed  []providerTask `json:"removed"`
}

func (s *twoInstanceStream) frame(t *testing.T, value any) {
	t.Helper()
	if !s.reader.Scan() {
		t.Fatal("missing SSE frame", s.reader.Err())
	}
	line := s.reader.Text()
	if !strings.HasPrefix(line, "data: ") || !s.reader.Scan() || s.reader.Text() != "" {
		t.Fatal("invalid SSE framing", line, s.reader.Err())
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), value); err != nil {
		t.Fatal(err)
	}
}

func (s *twoInstanceStream) message(t *testing.T) twoInstanceMessage {
	t.Helper()
	var message twoInstanceMessage
	s.frame(t, &message)
	return message
}

func (s *twoInstanceStream) full(t *testing.T, want ...providerTask) {
	t.Helper()
	var result twoInstanceResult
	s.frame(t, &result)
	assertTwoInstanceFull(t, result, want)
}

func assertTwoInstanceFull(t *testing.T, result twoInstanceResult, want []providerTask) {
	t.Helper()
	if !result.Success || !result.Ready || !result.Authorized || result.ChangeSet != nil || !reflect.DeepEqual(result.Data, want) {
		t.Fatalf("full authorized baseline = %+v, want %+v without changes", result, want)
	}
}

func (s *twoInstanceStream) ended(t *testing.T, abrupt bool) {
	t.Helper()
	if s.reader.Scan() {
		t.Fatal("unexpected frame after instance shutdown", s.reader.Text())
	}
	err := s.reader.Err()
	if err != nil && (!abrupt || !errors.Is(err, io.ErrUnexpectedEOF)) {
		t.Fatal("stream did not terminate at instance shutdown", err)
	}
}

func twoInstanceControl(t *testing.T, router *httptest.Server, headers http.Header, connectionID, action, queryID string, revision, status int) {
	t.Helper()
	body := map[string]any{"connectionId": connectionID, "queryId": queryID, "revision": revision}
	if action == "subscribe" {
		body["request"] = map[string]any{"queryName": "providerTask.Observe", "transferMode": "delta"}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, router.URL+twoInstanceHubPath+"/"+action, strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = headers.Clone()
	request.Header.Set("Content-Type", "application/json")
	response, err := router.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil || response.StatusCode != status || len(data) != 0 {
		t.Fatalf("%s status/body = %d/%q, want %d/empty: %v", action, response.StatusCode, data, status, err)
	}
}

func (s *twoInstanceStream) hubResults(t *testing.T, revision int, full bool, want providerTask, queryIDs ...string) {
	t.Helper()
	remaining := make(map[string]bool, len(queryIDs))
	for _, id := range queryIDs {
		remaining[id] = true
	}
	for range queryIDs {
		message := s.message(t)
		if message.Type != "QueryResult" || !remaining[message.QueryID] || message.Revision != revision {
			t.Fatal("unexpected/duplicate multiplexed delivery", message)
		}
		delete(remaining, message.QueryID)
		var result twoInstanceResult
		if err := json.Unmarshal(message.Payload, &result); err != nil {
			t.Fatal(err)
		}
		if full {
			assertTwoInstanceFull(t, result, []providerTask{want})
		} else if !result.Success || !result.Ready || !result.Authorized || result.Data != nil || result.ChangeSet == nil || len(result.ChangeSet.Added) != 0 || len(result.ChangeSet.Removed) != 0 || !reflect.DeepEqual(result.ChangeSet.Replaced, []providerTask{want}) {
			t.Fatalf("delta did not use this subscription's delivered baseline: %+v", result)
		}
	}
}

func assertTwoInstanceWatchersJoined(t *testing.T, node *twoInstanceNode, databases int) {
	t.Helper()
	node.stop(t)
	assertLiveWatchProfile(t, node.provider.record, databases)
	if got := countCommands(node.provider.record.snapshot(), "killCursors"); got != databases {
		t.Fatalf("joined database cursor closes = %d, want %d", got, databases)
	}
}

func twoInstanceRow(id int32, name, owner string) bson.D {
	return bson.D{{Key: "_id", Value: id}, {Key: "Name", Value: name}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: owner}}
}

func changeTwoInstanceRow(t *testing.T, node *twoInstanceNode, tenant tenancy.ID, collection string, row providerTask) {
	t.Helper()
	result, err := node.provider.collection(t, tenant, collection).UpdateOne(t.Context(), bson.D{{Key: "_id", Value: row.ID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "Name", Value: row.Name}}}})
	if err != nil || result.MatchedCount != 1 || result.ModifiedCount != 1 {
		t.Fatal("required acknowledged row mutation", result, err)
	}
}
