//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
)

// Deadlines below are failure bounds, never evidence that an event did not occur.
func liveReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("required live observation signal did not arrive")
		var zero T
		return zero
	}
}
func liveClose(t *testing.T, owner interface{ Close(context.Context) error }) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	if err := owner.Close(ctx); err != nil {
		t.Error("live owner cleanup", err)
	}
}
func liveWatcher(t *testing.T, f *providerFixture, config mongodb.WatcherOptions) *mongodb.Watcher {
	t.Helper()
	w, err := mongodb.NewWatcher(t.Context(), f.client, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { liveClose(t, w) })
	return w
}

type liveAuthority struct {
	member, policy atomic.Bool
	rowCalls       atomic.Int32
	// Row visibility can change without changing the captured principal.
	owner atomic.Value
}

func newLiveAuthority() *liveAuthority {
	a := &liveAuthority{}
	a.member.Store(true)
	a.policy.Store(true)
	a.owner.Store("")
	return a
}

// Both names use the SAME registered renderer, release and interceptor. No
// callback refetch and no provider-owned rendering scope exists in this fixture.
func liveObservationPipeline[T any](t *testing.T, f *providerFixture, w *mongodb.Watcher, name string, ownership mongodb.Ownership, a *liveAuthority, release func(context.Context, []bson.Raw) ([]T, error), intercept queries.InterceptorFunc[T]) queries.ObservablePipeline {
	t.Helper()
	binding, err := mongodb.NewCollection[T](f.client, mongodb.CollectionOptions{Database: f.base, Name: name, Ownership: ownership, SortFields: []queries.SortField{"id", "name"}})
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := mongodb.NewRenderer(binding, mongodb.RendererOptions[T]{RowFilter: func(_ context.Context, q queries.QueryContext) (bson.D, error) {
		a.rowCalls.Add(1)
		owner := a.owner.Load().(string)
		if owner == "" {
			owner = q.Principal().ID()
		}
		return bson.D{{Key: "OwnerID", Value: owner}}, nil
	}, Release: release})
	if err != nil {
		t.Fatal(err)
	}
	var r queries.Registry
	if err := queries.RegisterRenderer[mongodb.Find[T], []T](&r, func(context.Context, *execution.Scope) (queries.Renderer[mongodb.Find[T], []T], error) {
		return renderer, nil
	}); err != nil {
		t.Fatal(err)
	}
	declaration := metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "live"}}}
	if err := queries.Register[T](&r, "All", queries.Function(func(context.Context, queries.NoArguments) (mongodb.Find[T], error) {
		return mongodb.Find[T]{Filter: activeFilter()}, nil
	}), queries.WithAuthorization[queries.NoArguments](declaration)); err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterObservable[T, queries.NoArguments, mongodb.Find[T]](&r, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[mongodb.Find[T]], error) {
		return mongodb.Observe(w, binding, mongodb.Find[T]{Filter: activeFilter()})
	}), queries.WithAuthorization[queries.NoArguments](declaration), queries.WithCollectionIdentity[queries.NoArguments](func(row T) (any, error) {
		return reflect.ValueOf(row).FieldByName("ID").Interface(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if intercept != nil {
		if err := queries.RegisterReadModelInterceptor[T](&r, "live interception", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[T], error) {
			return intercept, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	var policies authorization.Registry
	if err := policies.Register("live", authorization.PolicyFunc(func(context.Context, authorization.Context) (authorization.Decision, error) {
		if a.policy.Load() {
			return authorization.Allow(), nil
		}
		return authorization.Deny("private revoked policy"), nil
	}), authorization.PolicyOptions{}); err != nil {
		t.Fatal(err)
	}
	evaluator, err := policies.Build(r.Catalog(), authorization.Options{})
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := r.Build(queries.PipelineOptions{Authorization: evaluator, RequireTenant: true, Membership: tenancy.MembershipFunc(func(_ context.Context, p identity.Principal, tenant tenancy.ID) (bool, error) {
		return a.member.Load() && p.IsAuthenticated() && (tenant == f.a || tenant == f.b), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	p := pipeline.(queries.ObservablePipeline)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := p.CloseObservations(ctx); err != nil {
			t.Error(err)
		}
	})
	return p
}
func liveContext(t *testing.T, tenant tenancy.ID, principal string) context.Context {
	t.Helper()
	return tenancy.WithTenant(identity.WithPrincipal(t.Context(), identity.NewPrincipal(identity.PrincipalData{ID: principal, AuthenticationType: "live provider fixture"})), tenant)
}
func liveName[T any](name string) queries.FullyQualifiedQueryName {
	return queries.FullyQualifiedQueryName(reflect.TypeFor[T]().Name() + "." + name)
}

var errLiveDeliveryDone = errors.New("live test delivery complete")

type liveDelivery struct {
	results     chan queries.Result[any]
	ack         chan error
	done        chan error
	observation *queries.Observation
}

func runLiveObservation[T any](t *testing.T, ctx context.Context, p queries.ObservablePipeline, params queries.Parameters, mode queries.TransferMode) *liveDelivery {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	o, admission, err := p.Open(ctx, liveName[T]("Observe"), queries.RequestFor(queries.NoArguments{}, params))
	if err != nil || o == nil || !admission.IsSuccess() {
		t.Fatal("live admission", admission.Details(), err)
	}
	d := &liveDelivery{make(chan queries.Result[any], 1), make(chan error), make(chan error, 1), o}
	go func() {
		d.done <- o.Run(ctx, queries.ObservationOptions{TransferMode: mode}, func(result queries.Result[any]) error {
			select {
			case d.results <- result:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case err := <-d.ack:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	t.Cleanup(func() {
		// Closing the observation cancels the callback and joins Run before scope disposal.
		cancel()
		liveClose(t, o)
	})
	return d
}
func (d *liveDelivery) acknowledge(t *testing.T, err error) {
	t.Helper()
	select {
	case d.ack <- err:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery acknowledgement did not join")
	}
}
func compareLiveUnary[T any](t *testing.T, ctx context.Context, p queries.ObservablePipeline, params queries.Parameters, result queries.Result[any]) []T {
	t.Helper()
	unary, err := queries.Perform[[]T](ctx, p, liveName[T]("All"), queries.RequestFor(queries.NoArguments{}, params))
	want, present := unary.Data()
	got, delivered := result.Data()
	if err != nil || !unary.IsSuccess() || !result.IsSuccess() || !present || !delivered || !reflect.DeepEqual(got, want) || result.Details().Paging != unary.Details().Paging {
		t.Fatalf("observable differs from equivalent authorized unary: got %+v (%v), want %+v (%v), err %v", got, result.Details(), want, unary.Details(), err)
	}
	return want
}
func isWatchCommand(command *event.CommandStartedEvent) bool {
	if command.CommandName != "aggregate" {
		return false
	}
	pipeline, ok := command.Command.Lookup("pipeline").ArrayOK()
	return ok && len(pipeline) > 0 && pipeline.Index(0).Document().Lookup("$changeStream").Type == bson.TypeEmbeddedDocument
}
func watchCommands(record *commandRecord) []event.CommandStartedEvent {
	var result []event.CommandStartedEvent
	for _, command := range record.snapshot() {
		if isWatchCommand(&command) {
			result = append(result, command)
		}
	}
	return result
}
func assertLiveWatchProfile(t *testing.T, record *commandRecord, expected int) {
	t.Helper()
	commands := watchCommands(record)
	if len(commands) != expected {
		t.Fatalf("database watch aggregates %d want %d", len(commands), expected)
	}
	for _, command := range commands {
		pipeline := command.Command.Lookup("pipeline").Array()
		values, err := pipeline.Values()
		if err != nil || len(values) != 2 || command.Command.Lookup("aggregate").AsInt64() != 1 || command.Command.Lookup("cursor").Document().Lookup("batchSize").AsInt64() != 64 {
			t.Fatal("ordinary database watch profile", command.Command)
		}
		stage := values[0].Document().Lookup("$changeStream").Document()
		fields, err := stage.Elements()
		if err != nil || len(fields) != 0 {
			t.Fatal("resume/fullDocument options must be absent", stage)
		}
		projection := values[1].Document().Lookup("$project").Document()
		fields, err = projection.Elements()
		if err != nil || len(fields) != 3 {
			t.Fatal("metadata projection", projection)
		}
		for _, key := range []string{"operationType", "ns", "to"} {
			if projection.Lookup(key).AsInt64() != 1 {
				t.Fatal("marker metadata projection", projection)
			}
		}
	}
}

// A monitor barrier on count keeps the real initial render blocked after the
// watch cursor is established. Mutation acknowledgement is a database signal;
// releasing the barrier does not clear the queued change-stream invalidation.
func TestLiveObservationInitialRenderAndBlockedDeliveryKeepInvalidations(t *testing.T) {
	f := liveProvider(t)
	const name = "ObservedTasks"
	collection := f.collection(t, f.a, name)
	insertRows(t, collection, []any{bson.D{{Key: "_id", Value: int32(1)}, {Key: "Name", Value: "A"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}}})
	w := liveWatcher(t, f, mongodb.WatcherOptions{})
	a := newLiveAuthority()
	p := liveObservationPipeline[providerTask](t, f, w, name, mongodb.ApplicationOwned, a, nil, queries.InterceptorFunc[providerTask](func(_ context.Context, row providerTask) (providerTask, error) {
		row.Name += " intercepted"
		return row, nil
	}))
	ctx := liveContext(t, f.a, "reader")
	params := paged(0, 3)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.record.mu.Lock()
	f.record.started = func(ctx context.Context, command *event.CommandStartedEvent) {
		if command.CommandName == "aggregate" && !isWatchCommand(command) && command.DatabaseName == collection.Database().Name() {
			once.Do(func() {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
	}
	f.record.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f.record.reset()
	d := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
	liveReceive(t, entered)
	assertLiveWatchProfile(t, f.record, 1)
	commands := f.record.snapshot()
	seenWatch := false
	for _, command := range commands {
		if isWatchCommand(&command) {
			seenWatch = true
		}
		if command.CommandName == "aggregate" && !isWatchCommand(&command) && command.DatabaseName == collection.Database().Name() && !seenWatch {
			t.Fatal("baseline read preceded cursor establishment")
		}
	}
	insertRows(t, collection, []any{bson.D{{Key: "_id", Value: int32(2)}, {Key: "Name", Value: "B"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}}})
	close(release)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
	d.acknowledge(t, nil)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
	// Delivery remains blocked while the watcher consumes another real mutation.
	if _, err := collection.UpdateOne(t.Context(), bson.D{{Key: "_id", Value: int32(2)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "Name", Value: "0"}}}}); err != nil {
		t.Fatal(err)
	}
	d.acknowledge(t, nil)
	rows := compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
	if len(rows) != 2 || rows[0].ID != 2 || a.rowCalls.Load() != 6 {
		t.Fatal("queued delivery/fresh RowFilter", rows, a.rowCalls.Load())
	}
	d.acknowledge(t, errLiveDeliveryDone)
	if !errors.Is(liveReceive(t, d.done), errLiveDeliveryDone) {
		t.Fatal("run did not finish")
	}
	liveClose(t, d.observation)
	liveClose(t, w)
	assertLiveWatchProfile(t, f.record, 1)
	if countCommands(f.record.snapshot(), "killCursors") != 1 {
		t.Fatal("database cursor was not disposed once")
	}
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal("borrowed client disposed", err)
	}
}

func TestLiveObservationMutationsCompareEveryCandidateWithUnary(t *testing.T) {
	f := liveProvider(t)
	const name = "ObservedMutationTasks"
	collection := f.collection(t, f.a, name)
	var rows []any
	for i, name := range []string{"A", "B", "C", "D", "A0"} {
		rows = append(rows, bson.D{{Key: "_id", Value: int32(i + 1)}, {Key: "Name", Value: name}, {Key: "Active", Value: i != 4}, {Key: "OwnerID", Value: "reader"}})
	}
	insertRows(t, collection, rows)
	w := liveWatcher(t, f, mongodb.WatcherOptions{})
	a := newLiveAuthority()
	p := liveObservationPipeline[providerTask](t, f, w, name, mongodb.ApplicationOwned, a, nil, nil)
	ctx := liveContext(t, f.a, "reader")
	params := paged(0, 3)
	d := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
	check := func(ids []int32, total int32) {
		t.Helper()
		result := liveReceive(t, d.results)
		rows := compareLiveUnary[providerTask](t, ctx, p, params, result)
		var got []int32
		for _, row := range rows {
			got = append(got, row.ID)
		}
		if !reflect.DeepEqual(got, ids) || int32(result.Details().Paging.TotalItems) != total {
			t.Fatal("page/refill/count", got, result.Details().Paging)
		}
	}
	update := func(id int32, fields bson.D) {
		t.Helper()
		if _, err := collection.UpdateOne(t.Context(), bson.D{{Key: "_id", Value: id}}, bson.D{{Key: "$set", Value: fields}}); err != nil {
			t.Fatal(err)
		}
	}
	check([]int32{1, 2, 3}, 4)
	cases := []struct {
		name   string
		mutate func()
		ids    []int32
		total  int32
	}{
		{"insert", func() {
			insertRows(t, collection, []any{bson.D{{Key: "_id", Value: int32(6)}, {Key: "Name", Value: "AA"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}}})
		}, []int32{1, 6, 2}, 5},
		{"off page update", func() { update(4, bson.D{{Key: "Name", Value: "Z"}}) }, []int32{1, 6, 2}, 5},
		{"reorder", func() { update(3, bson.D{{Key: "Name", Value: "0"}}) }, []int32{3, 1, 6}, 5},
		{"filter exit", func() { update(1, bson.D{{Key: "Active", Value: false}}) }, []int32{3, 6, 2}, 4},
		{"filter entry", func() { update(5, bson.D{{Key: "Active", Value: true}}) }, []int32{3, 5, 6}, 5},
		{"delete refill", func() {
			if _, err := collection.DeleteOne(t.Context(), bson.D{{Key: "_id", Value: int32(6)}}); err != nil {
				t.Fatal(err)
			}
		}, []int32{3, 5, 2}, 4},
		{"row authorization exit", func() { update(2, bson.D{{Key: "OwnerID", Value: "other"}}) }, []int32{3, 5, 4}, 3},
		{"replace", func() {
			if _, err := collection.ReplaceOne(t.Context(), bson.D{{Key: "_id", Value: int32(4)}}, bson.D{{Key: "_id", Value: int32(4)}, {Key: "Name", Value: "-1"}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}}); err != nil {
				t.Fatal(err)
			}
		}, []int32{4, 3, 5}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.mutate(); d.acknowledge(t, nil); check(tc.ids, tc.total) })
	}
	// Re-evaluate a changed RowFilter on the next marker, not a cached predicate.
	a.owner.Store("other")
	update(4, bson.D{{Key: "Name", Value: "changed"}})
	d.acknowledge(t, nil)
	check([]int32{2}, 1)
	d.acknowledge(t, errLiveDeliveryDone)
	if !errors.Is(liveReceive(t, d.done), errLiveDeliveryDone) {
		t.Fatal("run completion")
	}
	if a.rowCalls.Load() != 20 {
		t.Fatal("RowFilter not fresh for every observable/unary candidate", a.rowCalls.Load())
	}
	assertLiveWatchProfile(t, f.record, 1)
}

func TestLiveObservationRevocationBeforeCandidatePreventsRenderIO(t *testing.T) {
	for _, kind := range []string{"membership", "policy"} {
		t.Run(kind, func(t *testing.T) {
			f := liveProvider(t)
			name := "ObservedRevocation" + kind
			collection := f.collection(t, f.a, name)
			insertRows(t, collection, authorRows(1, 2))
			w := liveWatcher(t, f, mongodb.WatcherOptions{})
			a := newLiveAuthority()
			p := liveObservationPipeline[providerTask](t, f, w, name, mongodb.ApplicationOwned, a, nil, nil)
			ctx := liveContext(t, f.a, "reader")
			params := paged(0, 2)
			d := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
			compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
			before := a.rowCalls.Load()
			f.record.reset()
			if kind == "membership" {
				a.member.Store(false)
			} else {
				a.policy.Store(false)
			}
			if _, err := collection.UpdateOne(t.Context(), bson.D{{Key: "_id", Value: int32(1)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "Name", Value: "revoked"}}}}); err != nil {
				t.Fatal(err)
			}
			d.acknowledge(t, nil)
			denied := liveReceive(t, d.results)
			if denied.IsSuccess() || denied.Details().Authorized || denied.Details().Paging != (queries.PagingInfo{}) || denied.Details().ChangeSet != nil {
				t.Fatal("denial leaked result", denied.Details())
			}
			if _, ok := denied.Data(); ok {
				t.Fatal("denial published data")
			}
			for _, command := range f.record.snapshot() {
				if command.CommandName == "find" || command.CommandName == "aggregate" {
					t.Fatal("revoked candidate performed render I/O", command.Command)
				}
			}
			if a.rowCalls.Load() != before {
				t.Fatal("revoked candidate reached RowFilter")
			}
			d.acknowledge(t, nil)
			if err := liveReceive(t, d.done); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Synthetic protected BSON establishes release-before-interception on EVERY
// candidate, not real Chronicle SDK/sink/compliance compatibility.
func TestLiveObservationSyntheticReleaseAndInterceptionMatchUnary(t *testing.T) {
	f := liveProvider(t)
	const name = "ObservedSyntheticRelease"
	collection := f.collection(t, f.a, name)
	insertRows(t, collection, []any{bson.D{{Key: "_id", Value: int32(1)}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{9, 0, 8}}}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}, {Key: "Lineage", Value: "synthetic"}}})
	w := liveWatcher(t, f, mongodb.WatcherOptions{})
	a := newLiveAuthority()
	var releases, interceptions atomic.Int32
	release := func(_ context.Context, raw []bson.Raw) ([]providerTask, error) {
		releases.Add(1)
		var rows []providerTask
		for _, document := range raw {
			if document.Lookup("Name").Type != bson.TypeBinary || document.Lookup("Lineage").StringValue() != "synthetic" {
				return nil, fmt.Errorf("missing complete synthetic protected BSON")
			}
			rows = append(rows, providerTask{ID: document.Lookup("_id").Int32(), Name: "released", Active: true})
		}
		return rows, nil
	}
	p := liveObservationPipeline[providerTask](t, f, w, name, mongodb.ChronicleOwned, a, release, queries.InterceptorFunc[providerTask](func(_ context.Context, row providerTask) (providerTask, error) {
		interceptions.Add(1)
		if row.Name != "released" {
			return row, errors.New("interception before release")
		}
		row.Name += " intercepted"
		return row, nil
	}))
	ctx := liveContext(t, f.a, "reader")
	params := paged(0, 2)
	params.Sorting = queries.Sorting{}
	d := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
	if _, err := collection.UpdateOne(t.Context(), bson.D{{Key: "_id", Value: int32(1)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{3, 2, 1}}}}}}); err != nil {
		t.Fatal(err)
	}
	d.acknowledge(t, nil)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
	d.acknowledge(t, errLiveDeliveryDone)
	if !errors.Is(liveReceive(t, d.done), errLiveDeliveryDone) {
		t.Fatal("completion")
	}
	if releases.Load() != 4 || interceptions.Load() != 4 {
		t.Fatal("release/interception bypass", releases.Load(), interceptions.Load())
	}
}

func assertLiveTerminal(t *testing.T, result queries.Result[any]) {
	t.Helper()
	encoded := fmt.Sprint(result.Details())
	if result.IsSuccess() || !result.HasExceptions() || result.Details().Paging != (queries.PagingInfo{}) || result.Details().ChangeSet != nil || strings.Contains(encoded, "Resnapshot") || strings.Contains(encoded, "ChangeStreamHistoryLost") {
		t.Fatal("unsafe terminal", result.Details())
	}
	if _, ok := result.Data(); ok {
		t.Fatal("terminal published data")
	}
}
