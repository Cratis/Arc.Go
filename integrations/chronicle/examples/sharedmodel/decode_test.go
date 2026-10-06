// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sharedmodel_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/integrations/chronicle/examples/sharedmodel"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/projections"
)

func TestInventoryDescriptorDecodesSameDocumentNotePresence(t *testing.T) {
	registry := chronicle.NewRegistry()
	model, err := sharedmodel.RegisterChronicle(registry)
	must(t, err)
	client, err := chronicle.NewClient(chronicle.WithRegistry(registry))
	must(t, err)
	t.Cleanup(func() { must(t, client.Close()) })
	events, models, err := client.Catalogs("offline")
	must(t, err)
	descriptor, ok := models.LookupIdentifier(model.Identifier())
	if !ok {
		t.Fatal("Inventory descriptor missing")
	}
	note := "initial note"
	for _, tc := range []struct {
		name, property string
		want           *string
	}{
		{"initial", `,"note":"initial note"`, &note},
		{"null", `,"note":null`, nil},
		{"absent", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := []byte(`{"id":"item-1","product_name":"TenantB","URL_value":"https://example.test","state":"available"` + tc.property + `}`)
			decoded, err := descriptor.Unmarshal(document)
			must(t, err)
			var standard sharedmodel.Inventory
			must(t, json.Unmarshal(document, &standard))
			want := sharedmodel.Inventory{ID: "item-1", ProductName: "TenantB", URLValue: "https://example.test", Note: tc.want, State: "available"}
			if !reflect.DeepEqual(decoded, &want) || !reflect.DeepEqual(standard, want) {
				t.Fatalf("same-document descriptor=%+v standard=%+v want=%+v", decoded, standard, want)
			}
		})
	}

	clearDescriptor, ok := events.Lookup(sharedmodel.NoteCleared{})
	if !ok {
		t.Fatal("NoteCleared descriptor missing")
	}
	payload, err := clearDescriptor.Marshal(sharedmodel.NoteCleared{})
	must(t, err)
	assertJSON(t, payload, `{}`)
	if clearDescriptor.Ref().Generation != 1 {
		t.Fatal("clear event generation", clearDescriptor.Ref())
	}

	// Obtain typed handles for the same event identities; compile the actual
	// Inventory tags with the same options as RegisterChronicle, without I/O.
	handles := chronicle.NewRegistry()
	registered, err := chronicle.RegisterEvent[sharedmodel.ProductRegistered](handles)
	must(t, err)
	cleared, err := chronicle.RegisterEvent[sharedmodel.NoteCleared](handles)
	must(t, err)
	definition, err := projections.Compile(projections.ModelBound(model,
		projections.BindEvent("registered", registered), projections.BindEvent("cleared", cleared),
		projections.FromEvent(registered)), events)
	must(t, err)
	for _, from := range definition.KernelDefinition().From {
		if from.Key.Id == string(clearDescriptor.Ref().ID) {
			if from.Key.Generation != 1 || from.Value.Key != "$eventSourceId" || from.Value.Properties["note"] != "$null" {
				t.Fatalf("clear mapping = %+v", from)
			}
			return
		}
	}
	t.Fatal("clear event mapping missing")
}
