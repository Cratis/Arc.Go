//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/integrations/chronicle/examples/sharedmodel"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/observation"
	"github.com/cratis/chronicle.go/readmodels"
)

func TestOneModelProjectsAndServesArcNamespaceQuery(t *testing.T) {
	var model readmodels.Model[sharedmodel.Inventory]
	client, storeName, ctx := clientFor(t, func(registry *chronicle.Registry) {
		var err error
		model, err = sharedmodel.RegisterChronicle(registry)
		require(t, err)
	})
	builder, err := arc.NewBuilder(arc.Options{Namespace: "Shop"})
	require(t, err)
	require(t, sharedmodel.RegisterArc(builder, func(ctx context.Context) (sharedmodel.Reader, error) {
		tenant, _ := tenancy.TenantFrom(ctx)
		namespace := chronicle.DefaultNamespace
		if !tenant.IsDefault() {
			namespace = chronicle.Namespace(tenant.String())
		}
		store, err := client.EventStore(ctx, storeName, chronicle.WithNamespace(namespace))
		if err != nil {
			return nil, err
		}
		return readmodels.For(store.ReadModels(), model), nil
	}))
	app, err := builder.Build()
	require(t, err)
	require(t, app.Start(ctx))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require(t, app.Shutdown(cleanup))
	})
	for _, namespace := range []string{"Default", "TenantB"} {
		t.Run(namespace, func(t *testing.T) {
			store, err := client.EventStore(ctx, storeName, chronicle.WithNamespace(chronicle.Namespace(namespace)))
			require(t, err)
			logInventorySetup(t, store)
			defer func() {
				if t.Failed() || t.Skipped() {
					diagnoseInventory(t, ctx, store, model)
				}
			}()
			appended, err := store.EventLog().Append(ctx, "item-1", sharedmodel.ProductRegistered{
				DisplayName: sharedmodel.ProductName(namespace), URLValue: "https://example.test", Note: "initial note",
			})
			logInventoryAppend(t, store, "registered", appended, err)
			require(t, err)
			require(t, appended.Err())
			reader := readmodels.For(store.ReadModels(), model)
			note := "initial note"
			want := sharedmodel.Inventory{ID: "item-1", ProductName: sharedmodel.ProductName(namespace), URLValue: "https://example.test", Note: &note, State: "available"}
			awaitInventoryOrKnownStall(t, ctx, store, model, reader, want, appended.Position, namespace)
			appended, err = store.EventLog().Append(ctx, "item-1", sharedmodel.NoteCleared{})
			logInventoryAppend(t, store, "cleared", appended, err)
			require(t, err)
			require(t, appended.Err())
			want.Note = nil
			awaitInventoryOrKnownStall(t, ctx, store, model, reader, want, appended.Position, namespace)

			tenant, err := tenancy.ParseID(namespace)
			require(t, err)
			queryContext := tenancy.WithTenant(ctx, tenant)
			result, err := queries.Perform[sharedmodel.Inventory](queryContext, app.Queries(), "Shop.Inventory.ByID", queries.RequestFor(sharedmodel.ByID{ID: "item-1"}, queries.Parameters{}))
			require(t, err)
			got, present := result.Data()
			if !result.IsSuccess() || !present || !reflect.DeepEqual(got, want) {
				t.Fatal("Arc query differs from projected state", result, got)
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/inventory/by-id?id=item-1", nil).WithContext(queryContext)
			request.Header.Set("x-cratis-tenant-id", namespace)
			app.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatal(response.Code, response.Body.String())
			}
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			wire := `{"id":"item-1","product_name":"` + namespace + `","URL_value":"https://example.test","state":"available"}`
			var actual, expected map[string]any
			require(t, json.Unmarshal(envelope.Data, &actual))
			require(t, json.Unmarshal([]byte(wire), &expected))
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("Arc wire = %s, want %s", envelope.Data, wire)
			}
		})
	}
}

// awaitInventoryOrKnownStall waits for the projected inventory. On timeout it
// fails, except in the freshly ensured TenantB namespace where two upstream
// kernel defects are skipped for an active, subscribed projection observer with
// no failed partitions: the strand (Chronicle#4548), where the observer stays
// behind a known tail, and the skipped event (Chronicle#4583), where the
// observer advanced past the event but the read model was never written for it.
func awaitInventoryOrKnownStall(t *testing.T, ctx context.Context, store *chronicle.EventStore, model readmodels.Model[sharedmodel.Inventory], reader *readmodels.Reader[sharedmodel.Inventory], want sharedmodel.Inventory, position *events.SequenceNumber, namespace string) {
	t.Helper()
	materialized, waitElapsed := awaitInventory(t, ctx, reader, want)
	if materialized {
		return
	}
	if err := ctx.Err(); err != nil {
		t.Fatal("test context exhausted while waiting for projected inventory:", err)
	}
	failure := "projection did not materialize"
	if namespace != "TenantB" || position == nil {
		t.Fatal(failure)
	}
	for _, projection := range store.Projections() {
		if projection.Model().Identifier() != model.Identifier() {
			continue
		}
		stall, evidence := observeStall(t, ctx, store, observation.ID(projection.Identifier()), projection.EventSequence(), *position, 0, waitElapsed, failure)
		if stall.matchesChronicle4548() {
			knownKernelDefectObserved(t, chronicle4548+": catch-up job reuse strands observers in a freshly ensured namespace; re-enable with "+projectionStrandIssue, failure, evidence)
			return
		}
		lag, evidence := observeProjectionLag(t, ctx, reader, stall, evidence, failure)
		if lag.matchesChronicle4583() {
			knownKernelDefectObserved(t, chronicle4583+": catch-up advanced the projection observer past an event it never projected; re-enable with "+projectionStrandIssue, failure, evidence)
			return
		}
		t.Fatal(failure, evidence)
	}
	t.Fatal(failure, "; projection for the model not registered")
}

// observeProjectionLag reads the read model after the projection observer
// state, so a stale model is compared with a position the observer already
// reported, and fails the test when the model is unreadable.
func observeProjectionLag(t *testing.T, parent context.Context, reader *readmodels.Reader[sharedmodel.Inventory], stall reactorStall, evidence, failure string) (projectionLag, string) {
	t.Helper()
	read, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	instance, err := reader.Get(read, "item-1")
	if err != nil {
		t.Fatal(failure, evidence, "; read model unavailable:", err)
	}
	lag := projectionLag{Observer: stall, ModelExists: instance.Exists, ModelLastHandled: unavailable}
	if instance.LastHandled != nil {
		lag.ModelLastHandled = uint64(*instance.LastHandled)
	}
	return lag, fmt.Sprintf("%s model exists=%t LastHandled=%s value=%+v note=%s", evidence, instance.Exists, diagnosticValue(instance.LastHandled), instance.Value, diagnosticValue(instance.Value.Note))
}

// awaitInventory reports whether want materialized and whether the full
// 15-second window elapsed, rather than the parent context ending the wait.
func awaitInventory(t *testing.T, ctx context.Context, reader *readmodels.Reader[sharedmodel.Inventory], want sharedmodel.Inventory) (materialized, waitElapsed bool) {
	t.Helper()
	deadline, cancel := context.WithTimeoutCause(ctx, 15*time.Second, errObserverWaitElapsed)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	// last keeps the latest successful read: the read cut off by the window
	// boundary returns a zero instance that would hide the observed state.
	var last readmodels.Instance[sharedmodel.Inventory]
	for {
		instance, err := reader.Get(deadline, "item-1")
		if err != nil && !readEndedByWindow(deadline, err) {
			require(t, err)
		}
		if err == nil {
			if instance.Exists && reflect.DeepEqual(instance.Value, want) {
				return true, false
			}
			last = instance
		}
		select {
		case <-deadline.Done():
			t.Logf("last successful polling snapshot: exists=%t note=%s LastHandled=%s wantNote=%s", last.Exists, diagnosticValue(last.Value.Note), diagnosticValue(last.LastHandled), diagnosticValue(want.Note))
			t.Logf("projection did not materialize: got %+v, want %+v", last, want)
			return false, observerWaitElapsed(deadline)
		case <-ticker.C:
		}
	}
}
