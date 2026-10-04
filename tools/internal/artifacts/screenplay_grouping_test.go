// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
)

func TestScreenplayRejectsEmptyModelsAndNoInputCommands(t *testing.T) {
	for _, owner := range []string{"model", "command"} {
		for _, fields := range []struct {
			name  string
			value []FieldDescriptor
		}{{"nil fields", nil}, {"empty fields", []FieldDescriptor{}}} {
			t.Run(owner+"/"+fields.name, func(t *testing.T) {
				graph := screenplayGraph()
				node := &graph.Types[2]
				if owner == "command" {
					node = &graph.Types[0]
					graph.Commands[0].Fields = fields.value
				}
				node.Fields = fields.value
				before := screenplaySnapshot(t, graph)
				result, err := exportScreenplayMetadata(graph)
				if err == nil || !strings.Contains(err.Error(), "model requires at least one property") || len(result.Document) != 0 || len(result.Diagnostics) != 0 {
					t.Fatalf("result = %+v, error = %v; want empty-model rejection without bytes or diagnostics", result, err)
				}
				if !bytes.Equal(before, screenplaySnapshot(t, graph)) {
					t.Fatal("failed export changed input")
				}
			})
		}
	}
}

func screenplayInterleavedGraph() *Graph {
	command := metadata.Command{Type: metadata.TypeName{Namespace: "A", Name: "Register"}}
	listing := metadata.TypeName{Namespace: "A", Name: "Listing"}
	other := metadata.TypeName{Namespace: "A.Listing", Name: "Other"}
	first := metadata.Query{ReadModel: listing, Name: "A"}
	last := metadata.Query{ReadModel: listing, Name: "Z"}
	middle := metadata.Query{ReadModel: other, Name: "All"}
	input := []FieldDescriptor{{Name: "name", Type: WireType{Kind: "string"}}}
	return &Graph{
		FormatVersion: ContractGraphVersion,
		Profile:       ApplicationProfile{Name: "Tasks"},
		Catalog:       metadata.Catalog{Version: metadata.Version, Commands: []metadata.Command{command}, Queries: []metadata.Query{last, middle, first}},
		Types: []TypeDescriptor{
			{Key: "register", Name: command.Type, Kind: "model", Fields: input},
			{Key: "z-listing", Name: listing, Kind: "model", Fields: input},
			{Key: "a-other", Name: other, Kind: "model", Fields: []FieldDescriptor{{Name: "id", Type: WireType{Kind: "Guid"}}}},
		},
		Commands: []CommandDescriptor{{Declaration: command, TypeKey: "register", Input: &WireType{Kind: "model", Target: "register"}, Fields: input, ResponseKind: "none"}},
		Queries: []QueryDescriptor{
			{Declaration: last, TypeKey: "z-listing", Delivery: "snapshot", Result: WireType{Kind: "model", Target: "z-listing"}},
			{Declaration: middle, TypeKey: "a-other", Delivery: "snapshot", Result: WireType{Kind: "model", Target: "a-other"}},
			{Declaration: first, TypeKey: "z-listing", Delivery: "snapshot", Result: WireType{Kind: "model", Target: "z-listing"}},
		},
	}
}

func TestScreenplayGroupsNamespaceInterleavedQueriesDeterministically(t *testing.T) {
	graph := screenplayInterleavedGraph()
	before := screenplaySnapshot(t, graph)
	result, err := exportScreenplayMetadata(graph)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/screenplay/interleaved.play")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Document, golden) {
		t.Fatalf("document differs from pinned interleaving compiler fixture:\n%s", result.Document)
	}
	for _, model := range []string{"Listing", "Other"} {
		if strings.Count(string(result.Document), "            readmodel "+model+"\n") != 1 {
			t.Fatalf("read model %s was not emitted exactly once", model)
		}
	}
	if !bytes.Equal(before, screenplaySnapshot(t, graph)) {
		t.Fatal("export changed input")
	}
	slices.Reverse(graph.Queries)
	slices.Reverse(graph.Types)
	slices.Reverse(graph.Catalog.Queries)
	before = screenplaySnapshot(t, graph)
	again, err := exportScreenplayMetadata(graph)
	if err != nil || !bytes.Equal(result.Document, again.Document) || !reflect.DeepEqual(result.Diagnostics, again.Diagnostics) {
		t.Fatal("enumeration changed export", err)
	}
	if !bytes.Equal(before, screenplaySnapshot(t, graph)) {
		t.Fatal("reordered export changed input")
	}
}
