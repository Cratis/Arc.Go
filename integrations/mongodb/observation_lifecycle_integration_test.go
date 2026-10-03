//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
)

func observationFailpoint(t *testing.T, f *providerFixture, command string, code int32) {
	t.Helper()
	if err := f.client.Database("admin").RunCommand(t.Context(), bson.D{
		{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: bson.D{{Key: "times", Value: 1}}},
		{Key: "data", Value: bson.D{{Key: "failCommands", Value: bson.A{command}}, {Key: "appName", Value: "arc-provider-" + osOwner()}, {Key: "errorCode", Value: code}}},
	}).Err(); err != nil {
		t.Fatal("required observation failpoint", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 3*time.Second)
		defer cancel()
		if err := f.client.Database("admin").RunCommand(ctx, bson.D{{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: "off"}}).Err(); err != nil {
			t.Error(err)
		}
	})
}

// The command signal blocks the next ordinary getMore BEFORE its wire send.
// It is not a sleep or timeout-as-success; failCommand then injects actual server
// CursorNotFound/ChangeStreamHistoryLost replies. Natural oplog expiry is NOT tested.
func TestLiveObservationCursorLossIsTerminalNoResumeAndNewOpenIsFull(t *testing.T) {
	for _, code := range []int32{43, 286} {
		t.Run(map[int32]string{43: "cursor not found", 286: "injected history loss"}[code], func(t *testing.T) {
			f := liveProvider(t)
			name := fmt.Sprintf("ObservedCursorLoss%d", code)
			collection := f.collection(t, f.a, name)
			insertRows(t, collection, authorRows(1, 2))
			w := liveWatcher(t, f, mongodb.WatcherOptions{})
			a := newLiveAuthority()
			p := liveObservationPipeline[providerTask](t, f, w, name, mongodb.ApplicationOwned, a, nil, nil)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			f.record.mu.Lock()
			f.record.started = func(ctx context.Context, command *event.CommandStartedEvent) {
				if command.CommandName == "getMore" && command.DatabaseName == collection.Database().Name() {
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
			ctx := liveContext(t, f.a, "reader")
			params := paged(0, 2)
			d := runLiveObservation[providerTask](t, ctx, p, params, queries.Delta)
			first := liveReceive(t, d.results)
			compareLiveUnary[providerTask](t, ctx, p, params, first)
			if first.Details().ChangeSet != nil {
				t.Fatal("first Delta result must be full")
			}
			liveReceive(t, entered)
			observationFailpoint(t, f, "getMore", code)
			close(release)
			d.acknowledge(t, nil)
			assertLiveTerminal(t, liveReceive(t, d.results))
			d.acknowledge(t, nil)
			if err := liveReceive(t, d.done); !errors.Is(err, mongodb.ErrResnapshotRequired) {
				t.Fatal("lost local terminal identity", err)
			}
			liveClose(t, d.observation)
			join, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			err := mongodb.JoinFailedWatchForTest(join, w, collection.Database().Name())
			cancel()
			if err != nil {
				t.Fatal("failed generation cleanup did not join", err)
			}
			assertLiveWatchProfile(t, f.record, 1)
			if countCommands(f.record.snapshot(), "killCursors") != 1 {
				t.Fatal("ordinary failed cursor not cleaned")
			}
			// Explicit fresh Open on the SAME owner/pipeline, after the test-only join
			// signal, establishes a new cursor and independent transfer baseline.
			fresh := runLiveObservation[providerTask](t, ctx, p, params, queries.Delta)
			baseline := liveReceive(t, fresh.results)
			baselineRows := compareLiveUnary[providerTask](t, ctx, p, params, baseline)
			if baseline.Details().ChangeSet != nil {
				t.Fatal("new Open inherited old Delta baseline")
			}
			if _, err := collection.UpdateOne(t.Context(), bson.D{{Key: "_id", Value: int32(1)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "Name", Value: "changed"}}}}); err != nil {
				t.Fatal(err)
			}
			fresh.acknowledge(t, nil)
			change := liveReceive(t, fresh.results)
			if !change.IsSuccess() || change.Details().ChangeSet == nil {
				t.Fatal("fresh baseline not followed by Delta changes", change.Details())
			}
			if _, ok := change.Data(); ok {
				t.Fatal("Delta update unexpectedly full")
			}
			unary, err := queries.Perform[[]providerTask](ctx, p, liveName[providerTask]("All"), queries.RequestFor(queries.NoArguments{}, params))
			rows, present := unary.Data()
			wantChanges, computeErr := queries.ComputeChanges(baselineRows, rows)
			// NewResult normalizes empty slices; compare the public wire values,
			// not incidental nil/empty backing representations.
			gotJSON, encodeErr := json.Marshal(change.Details().ChangeSet)
			wantJSON, wantEncodeErr := json.Marshal(wantChanges)
			if err != nil || computeErr != nil || encodeErr != nil || wantEncodeErr != nil || !present || !unary.IsSuccess() || !bytes.Equal(gotJSON, wantJSON) || change.Details().Paging != unary.Details().Paging {
				t.Fatalf("fresh Delta differs from authorized unary: got %s want %s, errors %v %v %v %v", gotJSON, wantJSON, err, computeErr, encodeErr, wantEncodeErr)
			}
			fresh.acknowledge(t, errLiveDeliveryDone)
			if !errors.Is(liveReceive(t, fresh.done), errLiveDeliveryDone) {
				t.Fatal("fresh run completion")
			}
			assertLiveWatchProfile(t, f.record, 2)
		})
	}
}

func TestLiveObservationDropAndRenameTerminateWholeDatabaseGeneration(t *testing.T) {
	for _, operation := range []string{"drop", "rename"} {
		t.Run(operation, func(t *testing.T) {
			f := liveProvider(t)
			const name = "ObservedCoordinates"
			collection := f.collection(t, f.a, name)
			insertRows(t, collection, authorRows(1, 2))
			w := liveWatcher(t, f, mongodb.WatcherOptions{})
			p := liveObservationPipeline[providerTask](t, f, w, name, mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
			ctx := liveContext(t, f.a, "reader")
			params := paged(0, 2)
			d := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
			compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
			if operation == "drop" {
				if err := collection.Drop(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				namespace := collection.Database().Name() + "." + name
				if err := f.client.Database("admin").RunCommand(t.Context(), bson.D{{Key: "renameCollection", Value: namespace}, {Key: "to", Value: namespace + "Renamed"}}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			d.acknowledge(t, nil)
			assertLiveTerminal(t, liveReceive(t, d.results))
			d.acknowledge(t, nil)
			if !errors.Is(liveReceive(t, d.done), mongodb.ErrResnapshotRequired) {
				t.Fatal("coordinate loss not terminal")
			}
			assertLiveWatchProfile(t, f.record, 1)
		})
	}
}

func TestLiveObservationSlowSubscriberOverflowDoesNotStopIndependentReader(t *testing.T) {
	f := liveProvider(t)
	const name = "ObservedOverflow"
	collection := f.collection(t, f.a, name)
	insertRows(t, collection, authorRows(1, 2))
	w := liveWatcher(t, f, mongodb.WatcherOptions{Buffer: 1, MaxSubscribers: 2, MaxSubscribersPerQuery: 2})
	p := liveObservationPipeline[providerTask](t, f, w, name, mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
	getMore := make(chan struct{}, 16)
	var batch atomic.Bool
	f.record.mu.Lock()
	f.record.succeeded = func(_ context.Context, command *event.CommandSucceededEvent) {
		if command.CommandName == "getMore" {
			if values, ok := command.Reply.Lookup("cursor", "nextBatch").ArrayOK(); ok {
				items, err := values.Values()
				if err == nil && len(items) > 0 {
					batch.Store(true)
				}
			}
		}
	}
	f.record.started = func(_ context.Context, command *event.CommandStartedEvent) {
		if command.CommandName == "getMore" && command.DatabaseName == collection.Database().Name() && batch.Swap(false) {
			getMore <- struct{}{}
		}
	}
	f.record.mu.Unlock()
	ctx := liveContext(t, f.a, "reader")
	params := paged(0, 2)
	slow := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, slow.results))
	fast := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, fast.results))
	if extra, _, err := p.Open(ctx, liveName[providerTask]("Observe"), queries.RequestFor(queries.NoArguments{}, params)); extra != nil || !errors.Is(err, mongodb.ErrLimit) {
		t.Fatal("required live admission limit", err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := collection.UpdateOne(t.Context(), bson.D{{Key: "_id", Value: int32(1)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "Name", Value: []string{"", "one", "two"}[i]}}}}); err != nil {
			t.Fatal(err)
		}
		liveReceive(t, getMore) // Next wire read proves prior batch fanout completed.
		fast.acknowledge(t, nil)
		compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, fast.results))
	}
	slow.acknowledge(t, nil)
	assertLiveTerminal(t, liveReceive(t, slow.results))
	slow.acknowledge(t, nil)
	if !errors.Is(liveReceive(t, slow.done), observable.ErrOverflow) {
		t.Fatal("slow subscriber did not overflow")
	}
	fast.acknowledge(t, errLiveDeliveryDone)
	if !errors.Is(liveReceive(t, fast.done), errLiveDeliveryDone) {
		t.Fatal("independent subscriber failed")
	}
	assertLiveWatchProfile(t, f.record, 1)
}

func TestLiveObservationClientsTenantsPrincipalsModelsAndCollectionsAreIndependent(t *testing.T) {
	f := liveProvider(t)
	other := liveProvider(t)
	for _, tenant := range []struct {
		name  string
		first int32
	}{{"A", 1}, {"B", 101}} {
		id := f.a
		if tenant.name == "B" {
			id = f.b
		}
		insertRows(t, f.collection(t, id, "SharedTasks"), authorRows(tenant.first, 2))
		insertRows(t, f.collection(t, id, "OtherAuthors"), authorRows(tenant.first+20, 2))
	}
	w := liveWatcher(t, f, mongodb.WatcherOptions{})
	w2 := liveWatcher(t, other, mongodb.WatcherOptions{})
	p := liveObservationPipeline[providerTask](t, f, w, "SharedTasks", mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
	authors := liveObservationPipeline[snapshot.Author](t, f, w, "OtherAuthors", mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
	independent := liveObservationPipeline[providerTask](t, other, w2, "SharedTasks", mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
	params := paged(0, 3)
	ctxA := liveContext(t, f.a, "reader")
	ctxB := liveContext(t, f.b, "reader")
	ctxOther := liveContext(t, f.a, "other")
	a := runLiveObservation[providerTask](t, ctxA, p, params, queries.Full)
	compareLiveUnary[providerTask](t, ctxA, p, params, liveReceive(t, a.results))
	b := runLiveObservation[providerTask](t, ctxB, p, params, queries.Full)
	rows := compareLiveUnary[providerTask](t, ctxB, p, params, liveReceive(t, b.results))
	if rows[0].ID != 101 {
		t.Fatal("tenant isolation", rows)
	}
	o := runLiveObservation[providerTask](t, ctxOther, p, params, queries.Full)
	rows = compareLiveUnary[providerTask](t, ctxOther, p, params, liveReceive(t, o.results))
	if len(rows) != 1 || rows[0].ID != 3 {
		t.Fatal("principal isolation", rows)
	}
	m := runLiveObservation[snapshot.Author](t, ctxA, authors, params, queries.Full)
	compareLiveUnary[snapshot.Author](t, ctxA, authors, params, liveReceive(t, m.results))
	c := runLiveObservation[providerTask](t, ctxA, independent, params, queries.Full)
	compareLiveUnary[providerTask](t, ctxA, independent, params, liveReceive(t, c.results))
	assertLiveWatchProfile(t, f.record, 2)
	assertLiveWatchProfile(t, other.record, 1)
	if _, err := f.collection(t, f.a, "SharedTasks").UpdateOne(t.Context(), bson.D{{Key: "_id", Value: int32(1)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "OwnerID", Value: "other"}}}}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		d        *liveDelivery
		ctx      context.Context
		pipeline queries.ObservablePipeline
	}{{a, ctxA, p}, {o, ctxOther, p}, {c, ctxA, independent}} {
		entry.d.acknowledge(t, nil)
		compareLiveUnary[providerTask](t, entry.ctx, entry.pipeline, params, liveReceive(t, entry.d.results))
	}
	// Drive each other coordinate with its OWN mutation instead of asserting
	// silence via a timeout on the unrelated tenant/model/collection.
	for _, entry := range []struct {
		collection string
		ctx        context.Context
		first      int32
	}{{"SharedTasks", ctxB, 101}, {"OtherAuthors", ctxA, 21}} {
		tenant := f.a
		if entry.first == 101 {
			tenant = f.b
		}
		if _, err := f.collection(t, tenant, entry.collection).UpdateOne(t.Context(), bson.D{{Key: "_id", Value: entry.first}}, bson.D{{Key: "$set", Value: bson.D{{Key: "Name", Value: "independent"}}}}); err != nil {
			t.Fatal(err)
		}
		if entry.collection == "SharedTasks" {
			b.acknowledge(t, nil)
			compareLiveUnary[providerTask](t, entry.ctx, p, params, liveReceive(t, b.results))
		} else {
			m.acknowledge(t, nil)
			compareLiveUnary[snapshot.Author](t, entry.ctx, authors, params, liveReceive(t, m.results))
		}
	}
	for _, d := range []*liveDelivery{a, b, o, m, c} {
		d.acknowledge(t, errLiveDeliveryDone)
		if !errors.Is(liveReceive(t, d.done), errLiveDeliveryDone) {
			t.Fatal("independent run completion")
		}
	}
}

func TestLiveObservationCanceledStartupDoesNotDisposeBorrowedClient(t *testing.T) {
	f := liveProvider(t)
	w := liveWatcher(t, f, mongodb.WatcherOptions{})
	p := liveObservationPipeline[providerTask](t, f, w, "CanceledWatchStartup", mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
	entered := configureFailure(t, f, "aggregate", true)
	started := make(chan struct{}, 1)
	f.record.mu.Lock()
	f.record.started = func(_ context.Context, command *event.CommandStartedEvent) {
		if isWatchCommand(command) {
			started <- struct{}{}
		}
	}
	f.record.mu.Unlock()
	ctx, cancel := context.WithCancel(liveContext(t, f.a, "reader"))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		o, _, err := p.Open(ctx, liveName[providerTask]("Observe"), queries.Request{})
		if o != nil {
			err = errors.Join(err, o.Close(context.WithoutCancel(ctx)))
		}
		done <- err
	}()
	liveReceive(t, started)
	wait, stop := context.WithTimeout(t.Context(), 3*time.Second)
	err := f.client.Database("admin").RunCommand(wait, bson.D{{Key: "waitForFailPoint", Value: "failCommand"}, {Key: "timesEntered", Value: entered + 1}}).Err()
	stop()
	if err != nil {
		t.Fatal("startup block not entered", err)
	}
	cancel()
	if !errors.Is(liveReceive(t, done), context.Canceled) {
		t.Fatal("startup cancellation did not join its subscriber")
	}
	liveClose(t, w)
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal("startup disposed borrowed client", err)
	}
}
