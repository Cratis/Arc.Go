// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sharedmodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/integrations/chronicle/examples/sharedmodel"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/readmodels"
	"github.com/cratis/fundamentals.go/concepts"
)

type reader struct {
	instance readmodels.Instance[sharedmodel.Inventory]
	err      error
}

func (r reader) Get(_ context.Context, key readmodels.Key) (readmodels.Instance[sharedmodel.Inventory], error) {
	if key != "item-1" {
		return readmodels.Instance[sharedmodel.Inventory]{}, nil
	}
	return r.instance, r.err
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOneModelRegistersWithBothFrameworksWithoutKernel(t *testing.T) {
	registry := chronicle.NewRegistry()
	model, err := sharedmodel.RegisterChronicle(registry)
	must(t, err)
	client, err := chronicle.NewClient(chronicle.WithRegistry(registry))
	must(t, err)
	t.Cleanup(func() { must(t, client.Close()) })
	_, catalog, err := client.Catalogs("offline")
	must(t, err)
	descriptors := catalog.Descriptors()
	if len(descriptors) != 1 || descriptors[0].Identifier() != model.Identifier() {
		t.Fatalf("read-model catalog = %v", descriptors)
	}
	kind, producer := descriptors[0].Observer()
	if kind != readmodels.Projection || producer == "" {
		t.Fatal("no compiled model-bound projection producer", kind, producer)
	}

	representation, recognized, err := concepts.Underlying(reflect.TypeFor[sharedmodel.ProductName]())
	must(t, err)
	if !recognized {
		t.Fatal("ProductName is not a recognized concept")
	}
	must(t, concepts.CheckJSON(representation, []byte(`"Notebook"`)))
	note := "initial note"
	value := sharedmodel.Inventory{ID: "item-1", ProductName: "Notebook", URLValue: "https://example.test", Note: &note, State: "available", Transient: "not on the wire"}
	wire, err := descriptors[0].Marshal(value)
	must(t, err)
	want := `{"id":"item-1","product_name":"Notebook","URL_value":"https://example.test","note":"initial note","state":"available"}`
	assertJSON(t, wire, want)

	builder, err := arc.NewBuilder(arc.Options{Namespace: "Shop"})
	must(t, err)
	must(t, sharedmodel.RegisterArc(builder, func(context.Context) (sharedmodel.Reader, error) {
		return reader{instance: readmodels.Instance[sharedmodel.Inventory]{Exists: true, Value: value}}, nil
	}))
	app, err := builder.Build()
	must(t, err)
	must(t, app.Start(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		must(t, app.Shutdown(ctx))
	})
	result, err := queries.Perform[sharedmodel.Inventory](t.Context(), app.Queries(), "Shop.Inventory.ByID", queries.RequestFor(sharedmodel.ByID{ID: "item-1"}, queries.Parameters{}))
	must(t, err)
	got, present := result.Data()
	if !result.IsSuccess() || !present || !reflect.DeepEqual(got, value) {
		t.Fatal(result, got)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/inventory/by-id?id=item-1", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	must(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	assertJSON(t, envelope.Data, want)
}

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var actual, expected map[string]any
	must(t, json.Unmarshal(got, &actual))
	must(t, json.Unmarshal([]byte(want), &expected))
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("wire = %s, want %s", got, want)
	}
}

func TestNamespaceQueryPreservesAbsenceAndReaderFailure(t *testing.T) {
	_, err := (sharedmodel.Inventory{}).ByID(t.Context(), sharedmodel.ByID{ID: "item-1"}, reader{})
	if err == nil {
		t.Fatal("missing model returned as zero state")
	}
	failure := errors.New("read failed")
	_, err = (sharedmodel.Inventory{}).ByID(t.Context(), sharedmodel.ByID{ID: "item-1"}, reader{err: failure})
	if !errors.Is(err, failure) {
		t.Fatal("reader error lost", err)
	}
}
