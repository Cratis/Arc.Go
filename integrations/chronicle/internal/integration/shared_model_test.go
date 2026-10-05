//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"encoding/json"
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
			if namespace == "TenantB" {
				// A freshly ensured namespace can strand every event-log observer behind the tail.
				skipKnownKernelDefect(t, chronicle4548+": catch-up job reuse strands observers in a freshly ensured namespace; re-enable with https://github.com/Cratis/Arc.Go/issues/43")
			}
			store, err := client.EventStore(ctx, storeName, chronicle.WithNamespace(chronicle.Namespace(namespace)))
			require(t, err)
			logInventorySetup(t, store)
			defer func() {
				if t.Failed() {
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
			awaitInventory(t, ctx, reader, want)
			appended, err = store.EventLog().Append(ctx, "item-1", sharedmodel.NoteCleared{})
			logInventoryAppend(t, store, "cleared", appended, err)
			require(t, err)
			require(t, appended.Err())
			want.Note = nil
			awaitInventory(t, ctx, reader, want)

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

func awaitInventory(t *testing.T, ctx context.Context, reader *readmodels.Reader[sharedmodel.Inventory], want sharedmodel.Inventory) {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		instance, err := reader.Get(deadline, "item-1")
		require(t, err)
		if instance.Exists && reflect.DeepEqual(instance.Value, want) {
			return
		}
		select {
		case <-deadline.Done():
			t.Logf("last polling snapshot: exists=%t note=%s LastHandled=%s wantNote=%s", instance.Exists, diagnosticValue(instance.Value.Note), diagnosticValue(instance.LastHandled), diagnosticValue(want.Note))
			t.Fatalf("projection did not materialize: got %+v, want %+v", instance, want)
		case <-ticker.C:
		}
	}
}
