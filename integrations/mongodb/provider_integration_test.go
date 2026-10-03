//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func osOwner() string { return os.Getenv("ARC_MONGODB_TEST_OWNER") }

const providerImage = "mongo:8.0.15@sha256:f4d54619262ae3bc6a0a8efbebcef970b87b8ad70697479a75ce308a6f400158"

type commandRecord struct {
	mu       sync.Mutex
	commands []event.CommandStartedEvent
	started  func(context.Context, *event.CommandStartedEvent)
}

func (r *commandRecord) monitor() *event.CommandMonitor {
	return &event.CommandMonitor{Started: func(ctx context.Context, command *event.CommandStartedEvent) {
		copy := *command
		copy.Command = bytes.Clone(command.Command)
		r.mu.Lock()
		r.commands = append(r.commands, copy)
		callback := r.started
		r.mu.Unlock()
		if callback != nil {
			callback(ctx, command)
		}
	}}
}
func (r *commandRecord) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = nil
}
func (r *commandRecord) snapshot() []event.CommandStartedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.commands)
}

type providerFixture struct {
	client *mongo.Client
	record *commandRecord
	base   string
	a, b   tenancy.ID
}

func liveProvider(t *testing.T) *providerFixture {
	t.Helper()
	uri, owner := os.Getenv("ARC_MONGODB_TEST_URI"), os.Getenv("ARC_MONGODB_TEST_OWNER")
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "mongodb" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil || parsed.Query().Get("directConnection") != "true" || parsed.Query().Get("replicaSet") != "arc_provider" || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(owner) || os.Getenv("ARC_MONGODB_TEST_IMAGE") != providerImage || !strings.HasPrefix(os.Getenv("ARC_MONGODB_TEST_IMAGE_ID"), "sha256:") {
		t.Fatal("required task-owned replica-set harness URI/ownership/image evidence is missing or invalid; run scripts/replica-set-tests.py (never skip this lane)")
	}
	record := &commandRecord{}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetAppName("arc-provider-" + owner).SetRetryReads(false).SetServerSelectionTimeout(3 * time.Second).SetConnectTimeout(3 * time.Second).SetMonitor(record.monitor()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			t.Error(err)
		}
	})
	var build struct {
		Version string `bson:"version"`
	}
	if err := client.Database("admin").RunCommand(t.Context(), bson.D{{Key: "buildInfo", Value: 1}}).Decode(&build); err != nil || build.Version != "8.0.15" {
		t.Fatalf("required provider profile version: %+v, %v", build, err)
	}
	var hello struct {
		Primary bool   `bson:"isWritablePrimary"`
		Set     string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(t.Context(), bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil || !hello.Primary || hello.Set != "arc_provider" {
		t.Fatalf("required single-member replica-set profile: %+v, %v", hello, err)
	}
	a, err := tenancy.ParseID("A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := tenancy.ParseID("B")
	if err != nil {
		t.Fatal(err)
	}
	return &providerFixture{client: client, record: record, base: "ArcProvider_" + owner, a: a, b: b}
}
func (f *providerFixture) collection(t *testing.T, tenant tenancy.ID, name string) *mongo.Collection {
	t.Helper()
	database, err := mongodb.DatabaseName(f.base, tenant)
	if err != nil {
		t.Fatal(err)
	}
	return f.client.Database(database).Collection(name)
}
func verifiedReader() identity.Principal {
	return identity.NewPrincipal(identity.PrincipalData{ID: "reader", AuthenticationType: "provider fixture"})
}
func providerAuthentication() authentication.Handler {
	return authentication.HandlerFunc(func(_ context.Context, request *http.Request) (authentication.Result, error) {
		if request.Header.Get("Authorization") == "Bearer provider-fixture-reader" {
			return authentication.Authenticated(verifiedReader())
		}
		return authentication.Anonymous(), nil
	})
}
func providerMembership(a, b tenancy.ID) tenancy.Membership {
	return tenancy.MembershipFunc(func(_ context.Context, p identity.Principal, tenant tenancy.ID) (bool, error) {
		return p.ID() == "reader" && (tenant == a || tenant == b), nil
	})
}
func ownerFilter(_ context.Context, q queries.QueryContext) (bson.D, error) {
	if q.Principal().ID() != "reader" {
		return nil, errors.New("verified reader required")
	}
	return bson.D{{Key: "OwnerID", Value: q.Principal().ID()}}, nil
}

type providerResources struct {
	closes  atomic.Int32
	failure error
}

func (r *providerResources) Close(context.Context) error { r.closes.Add(1); return r.failure }

func registeredProvider[T any](t *testing.T, f *providerFixture, name string, ownership mongodb.Ownership, config mongodb.RendererOptions[T], filter bson.D, intercept queries.InterceptorFunc[T], resources *providerResources) *arc.Application {
	t.Helper()
	collection, err := mongodb.NewCollection[T](f.client, mongodb.CollectionOptions{Database: f.base, Name: name, Ownership: ownership, SortFields: []queries.SortField{"id", "name"}})
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := mongodb.NewRenderer(collection, config)
	if err != nil {
		t.Fatal(err)
	}
	if resources == nil {
		resources = &providerResources{}
	}
	builder, err := arc.NewBuilder(arc.Options{Authentication: []authentication.Handler{providerAuthentication()}, RequireTenant: true, Membership: providerMembership(f.a, f.b), OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
	if err != nil {
		t.Fatal(err)
	}
	err = queries.RegisterRenderer[mongodb.Find[T], []T](builder.Queries(), func(context.Context, *execution.Scope) (queries.Renderer[mongodb.Find[T], []T], error) {
		return renderer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = queries.Register[T](builder, "All", queries.Function(func(context.Context, queries.NoArguments) (mongodb.Find[T], error) {
		return mongodb.Find[T]{Filter: filter}, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{}), queries.WithPath[queries.NoArguments]("/rows"))
	if err != nil {
		t.Fatal(err)
	}
	if intercept != nil {
		err = queries.RegisterReadModelInterceptor[T](builder.Queries(), "after publication boundary", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[T], error) {
			return intercept, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	app, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	startProviderApp(t, app)
	return app
}
func startProviderApp(t *testing.T, app *arc.Application) {
	t.Helper()
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
}
func providerHTTP(t *testing.T, app *arc.Application, tenant tenancy.ID, path string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(t.Context())
	request.Header.Set("x-cratis-tenant-id", tenant.String())
	if authenticated {
		request.Header.Set("Authorization", "Bearer provider-fixture-reader")
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}
func typedSnapshot[T any](t *testing.T, app *arc.Application, tenant tenancy.ID, parameters queries.Parameters) (queries.Result[[]T], error) {
	t.Helper()
	ctx := tenancy.WithTenant(identity.WithPrincipal(t.Context(), verifiedReader()), tenant)
	return queries.Perform[[]T](ctx, app.Queries(), queries.FullyQualifiedQueryName(reflect.TypeFor[T]().Name()+".All"), queries.RequestFor(queries.NoArguments{}, parameters))
}
func paged(page, size int32) queries.Parameters {
	return queries.Parameters{Paging: queries.Paging{IsPaged: true, Page: queries.PageNumber(page), Size: queries.PageSize(size)}, Sorting: queries.Sorting{Field: "Name", Direction: queries.Ascending}}
}
func activeFilter() bson.D { return bson.D{{Key: "Active", Value: true}} }
func insertRows(t *testing.T, collection *mongo.Collection, rows []any) {
	t.Helper()
	if _, err := collection.InsertMany(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
}
func authorRows(first, count int32) []any {
	rows := make([]any, 0, count+2)
	for i := first; i < first+count; i++ {
		rows = append(rows, bson.D{{Key: "_id", Value: i}, {Key: "Name", Value: "same"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}})
	}
	return append(rows,
		bson.D{{Key: "_id", Value: first + count}, {Key: "Name", Value: "forbidden"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "other"}},
		bson.D{{Key: "_id", Value: first + count + 1}, {Key: "Name", Value: "inactive"}, {Key: "Active", Value: false}, {Key: "OwnerID", Value: "reader"}})
}

func TestLiveAuthorizedHTTPCountWindowTenantsAndCursorBatches(t *testing.T) {
	f := liveProvider(t)
	insertRows(t, f.collection(t, f.a, "Authors"), authorRows(1, 150))
	insertRows(t, f.collection(t, f.b, "Authors"), authorRows(1001, 150))
	resources := &providerResources{}
	app := registeredProvider[snapshot.Author](t, f, "Authors", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter}, activeFilter(), nil, resources)
	f.record.reset()
	response := providerHTTP(t, app, f.a, "/rows?page=1&pageSize=70&sortby=Name&sortDirection=asc", true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var envelope struct {
		Data    []snapshot.Author  `json:"data"`
		Paging  queries.PagingInfo `json:"paging"`
		Success bool               `json:"isSuccess"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Success || len(envelope.Data) != 70 || envelope.Data[0].ID != 71 || envelope.Data[69].ID != 140 || envelope.Paging != (queries.PagingInfo{Page: 1, Size: 70, TotalItems: 150}) || envelope.Paging.TotalPages() != 3 {
		t.Fatalf("window/count/ties/double paging: %+v", envelope)
	}
	assertProviderEnvelope(t, response, true)
	commands := f.record.snapshot()
	assertLivePushdown(t, commands, f.base+"+A", "Authors", 70, 70)
	if countCommands(commands, "getMore") == 0 {
		t.Fatal("required multiple cursor batches were not exercised")
	}
	for _, item := range envelope.Data {
		if item.Name != "same" || !item.Active {
			t.Fatal("forbidden/inactive row", item)
		}
	}
	if resources.closes.Load() != 1 {
		t.Fatal("operation holder not closed")
	}
	other, err := typedSnapshot[snapshot.Author](t, app, f.b, paged(1, 70))
	if err != nil {
		t.Fatal(err)
	}
	data, ok := other.Data()
	if !other.IsSuccess() || !ok || len(data) != 70 || data[0].ID != 1071 || other.Details().Paging.TotalItems != 150 {
		t.Fatal("tenant isolation", other.Details(), data)
	}
	for _, tenant := range []tenancy.ID{f.a, f.b} {
		empty, err := typedSnapshot[snapshot.Author](t, app, tenant, paged(99, 70))
		items, present := empty.Data()
		if err != nil || !empty.IsSuccess() || !present || items == nil || len(items) != 0 || empty.Details().Paging.TotalItems != 150 {
			t.Fatal("out-of-range page", empty.Details(), err)
		}
	}
	f.record.reset()
	denied := providerHTTP(t, app, f.a, "/rows?page=1&pageSize=70", false)
	if denied.Code != 403 || len(f.record.snapshot()) != 0 {
		t.Fatal("denied request performed DB operations", denied.Code, denied.Body.String())
	}
	assertProviderEnvelope(t, denied, false)
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal("borrowed client closed by scope", err)
	}
}

func countCommands(commands []event.CommandStartedEvent, name string) int {
	count := 0
	for _, command := range commands {
		if command.CommandName == name {
			count++
		}
	}
	return count
}
func assertLivePushdown(t *testing.T, commands []event.CommandStartedEvent, database, collection string, skip, limit int64) {
	t.Helper()
	var count, find *event.CommandStartedEvent
	for i := range commands {
		switch commands[i].CommandName {
		case "aggregate":
			count = &commands[i]
		case "find":
			find = &commands[i]
		}
	}
	if count == nil || find == nil || countCommands(commands, "aggregate") != 1 || countCommands(commands, "find") != 1 {
		t.Fatal("missing/surplus provider operations", commands)
	}
	match := count.Command.Lookup("pipeline").Array().Index(0).Document().Lookup("$match").Document()
	predicate := find.Command.Lookup("filter").Document()
	want, err := bson.Marshal(bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "OwnerID", Value: "reader"}}, activeFilter()}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(match, predicate) || !bytes.Equal(predicate, want) {
		t.Fatal("count and data did not share serialized frozen authorized predicate")
	}
	for _, command := range []*event.CommandStartedEvent{count, find} {
		if command.DatabaseName != database || command.Command.Lookup("collation").Document().Lookup("locale").StringValue() != "simple" || command.Command.Lookup("readConcern").Document().Lookup("level").StringValue() != "majority" {
			t.Fatal("provider coordinates/profile", command.Command)
		}
	}
	if !bytes.Equal(count.Command.Lookup("collation").Document(), find.Command.Lookup("collation").Document()) || count.Command.Lookup("aggregate").StringValue() != collection || find.Command.Lookup("find").StringValue() != collection {
		t.Fatal("count/find collection/collation mismatch")
	}
	if find.Command.Lookup("skip").AsInt64() != skip || find.Command.Lookup("limit").AsInt64() != limit || find.Command.Lookup("batchSize").AsInt64() != 64 {
		t.Fatal("window not pushed down", find.Command)
	}
	wantSort, err := bson.Marshal(bson.D{{Key: "Name", Value: int32(1)}, {Key: "_id", Value: int32(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(find.Command.Lookup("sort").Document(), wantSort) {
		t.Fatal("sort/tie breaker not pushed down", find.Command)
	}
}
func assertProviderEnvelope(t *testing.T, response *httptest.ResponseRecorder, success bool) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	keys := []string{"correlationId", "exceptionMessages", "exceptionStackTrace", "hasExceptions", "isAuthorized", "isReady", "isSuccess", "isValid", "paging", "validationResults"}
	if success {
		keys = append(keys, "data")
	}
	got := make([]string, 0, len(envelope))
	for key := range envelope {
		got = append(got, key)
	}
	slices.Sort(keys)
	slices.Sort(got)
	if !slices.Equal(keys, got) || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatal("exact Arc envelope", response.Header(), got)
	}
	var paging map[string]json.RawMessage
	if err := json.Unmarshal(envelope["paging"], &paging); err != nil {
		t.Fatal(err)
	}
	if len(paging) != 4 || paging["page"] == nil || paging["size"] == nil || paging["totalItems"] == nil || paging["totalPages"] == nil {
		t.Fatal("paging wire names", paging)
	}
	if !success && string(envelope["paging"]) != `{"page":0,"size":0,"totalItems":0,"totalPages":0}` {
		t.Fatal("failed request leaked paging", string(envelope["paging"]))
	}
	if success {
		var data []map[string]json.RawMessage
		if err := json.Unmarshal(envelope["data"], &data); err != nil {
			t.Fatal(err)
		}
		for _, row := range data {
			if len(row) != 3 || row["id"] == nil || row["name"] == nil || row["active"] == nil {
				t.Fatal("storage names leaked instead of JSON names", row)
			}
		}
	}
}

type providerTask struct {
	ID     int32  `json:"id" bson:"_id"`
	Name   string `json:"name" bson:"Name"`
	Active bool   `json:"active" bson:"Active"`
}

func TestLiveTaskRefetchInsertUpdateDeleteFilterEntryExitReorderAndRefill(t *testing.T) {
	f := liveProvider(t)
	collection := f.collection(t, f.a, "Tasks")
	rows := []any{}
	for i, name := range []string{"A", "B", "C", "D", "A0"} {
		rows = append(rows, bson.D{{Key: "_id", Value: int32(i + 1)}, {Key: "Name", Value: name}, {Key: "Active", Value: i != 4}, {Key: "OwnerID", Value: "reader"}})
	}
	insertRows(t, collection, rows)
	insertRows(t, f.collection(t, f.b, "Tasks"), authorRows(100, 2))
	app := registeredProvider[providerTask](t, f, "Tasks", mongodb.ApplicationOwned, mongodb.RendererOptions[providerTask]{RowFilter: ownerFilter}, activeFilter(), nil, nil)
	check := func(want []int32, total int64) {
		t.Helper()
		result, err := typedSnapshot[providerTask](t, app, f.a, paged(0, 3))
		items, present := result.Data()
		ids := make([]int32, len(items))
		for i := range items {
			ids[i] = items[i].ID
		}
		if err != nil || !present || !result.IsSuccess() || !slices.Equal(ids, want) || result.Details().Paging.TotalItems != total {
			t.Fatalf("refetch ids %v want %v total %d want %d err %v", ids, want, result.Details().Paging.TotalItems, total, err)
		}
	}
	update := func(id int32, fields bson.D) {
		t.Helper()
		if _, err := collection.UpdateOne(t.Context(), bson.D{{Key: "_id", Value: id}}, bson.D{{Key: "$set", Value: fields}}); err != nil {
			t.Fatal(err)
		}
	}
	check([]int32{1, 2, 3}, 4)
	insertRows(t, collection, []any{bson.D{{Key: "_id", Value: int32(6)}, {Key: "Name", Value: "AA"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}}})
	check([]int32{1, 6, 2}, 5)
	update(3, bson.D{{Key: "Name", Value: "0"}})
	check([]int32{3, 1, 6}, 5)
	update(1, bson.D{{Key: "Active", Value: false}})
	check([]int32{3, 6, 2}, 4)
	update(5, bson.D{{Key: "Active", Value: true}})
	check([]int32{3, 5, 6}, 5)
	if _, err := collection.DeleteOne(t.Context(), bson.D{{Key: "_id", Value: int32(6)}}); err != nil {
		t.Fatal(err)
	}
	check([]int32{3, 5, 2}, 4)
	update(2, bson.D{{Key: "OwnerID", Value: "other"}})
	check([]int32{3, 5, 4}, 3)
	other, err := typedSnapshot[providerTask](t, app, f.b, paged(0, 3))
	data, present := other.Data()
	if err != nil || !other.IsSuccess() || !present || len(data) != 2 || data[0].ID != 100 {
		t.Fatal("refetch affected other tenant", other.Details(), err)
	}
}

func TestLiveBoundsEmptyAndOuterScopeCleanupSuppressPublication(t *testing.T) {
	f := liveProvider(t)
	insertRows(t, f.collection(t, f.a, "Bounds"), authorRows(1, 4))
	for _, tc := range []struct {
		name       string
		max        int
		bytes      int64
		parameters queries.Parameters
	}{
		{"unpaged overflow", 3, 0, queries.Parameters{}},
		{"oversized page", 3, 0, paged(0, 4)},
		{"raw byte overflow", 10, 1, paged(0, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := registeredProvider[snapshot.Author](t, f, "Bounds", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter, MaxItems: tc.max, MaxBSONBytes: tc.bytes}, activeFilter(), nil, nil)
			f.record.reset()
			result, err := typedSnapshot[snapshot.Author](t, app, f.a, tc.parameters)
			if !errors.Is(err, mongodb.ErrLimit) {
				t.Fatal("lost inspectable bound failure", err)
			}
			assertNoProviderPublication(t, result, err)
			if tc.name == "oversized page" && len(f.record.snapshot()) != 0 {
				t.Fatal("oversized page performed DB operations")
			}
		})
	}
	empty := registeredProvider[snapshot.Author](t, f, "Empty", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter}, activeFilter(), nil, nil)
	result, err := typedSnapshot[snapshot.Author](t, empty, f.a, paged(0, 2))
	data, present := result.Data()
	if err != nil || !result.IsSuccess() || !present || data == nil || len(data) != 0 || result.Details().Paging.TotalItems != 0 {
		t.Fatal("empty page", result.Details(), err)
	}
	failure := errors.New("sensitive resource-close failure")
	resources := &providerResources{failure: failure}
	app := registeredProvider[snapshot.Author](t, f, "Bounds", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter}, activeFilter(), nil, resources)
	f.record.reset()
	result, err = typedSnapshot[snapshot.Author](t, app, f.a, paged(1, 2))
	if !errors.Is(err, failure) || resources.closes.Load() != 1 || countCommands(f.record.snapshot(), "find") != 1 {
		t.Fatal("required successful render then failing cleanup not exercised", err)
	}
	assertNoProviderPublication(t, result, err)
	response := providerHTTP(t, app, f.a, "/rows?page=1&pageSize=2", true)
	if response.Code != 500 {
		t.Fatal(response.Code, response.Body.String())
	}
	assertProviderEnvelope(t, response, false)
	if strings.Contains(response.Body.String(), "sensitive") {
		t.Fatal("private error leaked")
	}
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal("borrowed client was closed", err)
	}
}
func assertNoProviderPublication[T any](t *testing.T, result queries.Result[[]T], err error) {
	t.Helper()
	if err == nil || result.IsSuccess() {
		t.Fatal("provider failed open", result.Details(), err)
	}
	if _, present := result.Data(); present || result.Details().Paging != (queries.PagingInfo{}) || result.Details().ChangeSet != nil {
		t.Fatal("partial provider publication", result.Details())
	}
}
