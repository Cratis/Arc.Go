// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
)

func screenplayGraph() *Graph {
	command := metadata.Command{Type: metadata.TypeName{Namespace: "Tasks", Name: "Register"}}
	model := metadata.TypeName{Namespace: "Tasks", Name: "Listing"}
	all := metadata.Query{ReadModel: model, Name: "All"}
	watch := metadata.Query{ReadModel: model, Name: "Watch", Observable: true}
	input := []FieldDescriptor{{Name: "name", Type: WireType{Kind: "string"}}, {Name: "authorize", Type: WireType{Kind: "boolean"}}}
	return &Graph{
		FormatVersion: ContractGraphVersion,
		Profile:       ApplicationProfile{Name: "Tasks"},
		Packages:      []PackageDescriptor{{GoPath: "example/tasks", Namespace: "Tasks"}},
		Catalog:       metadata.Catalog{Version: metadata.Version, Commands: []metadata.Command{command}, Queries: []metadata.Query{all, watch}},
		Types: []TypeDescriptor{
			{Key: "register", Name: command.Type, Kind: "model", Fields: input},
			{Key: "listing", Name: model, Kind: "model", Fields: []FieldDescriptor{
				{Name: "id", Type: WireType{Kind: "Guid"}},
				{Name: "detail", Type: WireType{Kind: "model", Target: "detail"}},
				{Name: "tags", Type: WireType{Kind: "array", Element: &WireType{Kind: "string"}, Nullable: true}},
			}},
			{Key: "detail", Name: metadata.TypeName{Namespace: "Tasks", Name: "Detail"}, Kind: "model", Fields: []FieldDescriptor{
				{Name: "count", Type: WireType{Kind: "number", Contract: &WireContract{Scalar: &ScalarContract{GoKind: "int32"}}}},
				{Name: "enabled", Type: WireType{Kind: "optional", Element: &WireType{Kind: "boolean"}}},
				{Name: "amount", Type: WireType{Kind: "number", Contract: &WireContract{Scalar: &ScalarContract{GoKind: "float64"}}}},
				{Name: "at", Type: WireType{Kind: "Date", Contract: &WireContract{Declared: "time.Time"}}},
			}},
		},
		Commands: []CommandDescriptor{{Declaration: command, TypeKey: "register", Input: &WireType{Kind: "model", Target: "register"}, Fields: input, ResponseKind: "none"}},
		Queries: []QueryDescriptor{
			{Declaration: all, TypeKey: "listing", Delivery: "snapshot", Result: WireType{Kind: "array", Element: &WireType{Kind: "model", Target: "listing"}}},
			{Declaration: watch, TypeKey: "listing", Delivery: "observable", Result: WireType{Kind: "model", Target: "listing", Nullable: true}, Parameters: []FieldDescriptor{{Name: "prefix", Type: WireType{Kind: "string"}}}},
		},
	}
}

func screenplaySnapshot(t *testing.T, graph *Graph) []byte {
	t.Helper()
	value, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestScreenplayExportsPinnedDocumentAndPreservesInput(t *testing.T) {
	graph := screenplayGraph()
	before := screenplaySnapshot(t, graph)
	result, err := exportScreenplayMetadata(graph)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/screenplay/metadata.play")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Document, golden) {
		t.Fatalf("document differs from pinned compiler fixture:\n%s", result.Document)
	}
	if !bytes.Equal(before, screenplaySnapshot(t, graph)) {
		t.Fatal("export changed input")
	}
	if len(result.Diagnostics) != 5 {
		t.Fatalf("omission diagnostics = %v", result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if !bytes.Contains(result.Document, []byte("// "+diagnostic+"\n")) {
			t.Fatalf("document silently omitted diagnostic %q", diagnostic)
		}
	}
	// Returned data is caller-owned, not shared with another export or graph.
	result.Document[0] = '!'
	result.Diagnostics[0] = "changed"
	again, err := exportScreenplayMetadata(graph)
	if err != nil || !bytes.Equal(again.Document, golden) || !bytes.Equal(before, screenplaySnapshot(t, graph)) {
		t.Fatal("caller mutation escaped its result", err)
	}
}

func TestScreenplayRejectsAmbiguousOrUnrepresentableInputWithoutMutation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Graph)
		want   string
	}{
		{"duplicate package", func(g *Graph) { g.Packages = append(g.Packages, g.Packages[0]) }, "duplicate package"},
		{"duplicate type", func(g *Graph) { g.Types = append(g.Types, g.Types[0]) }, "duplicate type"},
		{"conflicting type name", func(g *Graph) { g.Types[2].Name = g.Types[1].Name }, "conflicting type name"},
		{"primitive name", func(g *Graph) { g.Types[2].Name.Name = "String" }, "conflicting type name"},
		{"duplicate command", func(g *Graph) { g.Commands = append(g.Commands, g.Commands[0]) }, "duplicate command"},
		{"duplicate query", func(g *Graph) { g.Queries = append(g.Queries, g.Queries[0]) }, "duplicate query"},
		{"missing declaration", func(g *Graph) { g.Queries = g.Queries[:1] }, "would be omitted"},
		{"duplicate catalog", func(g *Graph) { g.Catalog.Queries[1] = g.Catalog.Queries[0] }, "duplicate or conflicting catalog"},
		{"conflicting catalog", func(g *Graph) { g.Catalog.Commands[0].Path = "different" }, "conflicting catalog"},
		{"excluded command", func(g *Graph) { g.Commands[0].Excluded = true }, "excluded command"},
		{"excluded query", func(g *Graph) { g.Queries[0].Excluded = true }, "excluded query"},
		{"incomplete input", func(g *Graph) { g.Commands[0].Input = nil }, "normalized model input"},
		{"conflicting fields", func(g *Graph) { g.Commands[0].Fields = nil }, "conflicting command input"},
		{"missing read model", func(g *Graph) { g.Queries[0].TypeKey = "absent" }, "declared read model"},
		{"unknown delivery", func(g *Graph) { g.Queries[0].Delivery = "future" }, "query delivery"},
		{"invalid name", func(g *Graph) { g.Profile.Name = "Tasks\nmodule Injected" }, "ASCII identifier"},
		{"invalid property", func(g *Graph) { g.Types[2].Fields[0].Name = "Bad Name" }, "invalid or duplicate property"},
		{"duplicate property", func(g *Graph) { g.Types[2].Fields[1].Name = "count" }, "duplicate property"},
		{"number loses original type", func(g *Graph) { g.Types[2].Fields[0].Type.Contract = nil }, "Scalar.GoKind"},
		{"date loses original type", func(g *Graph) { g.Types[2].Fields[0].Type = WireType{Kind: "Date"} }, "original temporal descriptor"},
		{"map", func(g *Graph) { g.Types[2].Fields[0].Type.Kind = "record" }, "unsupported wire kind"},
		{"enum", func(g *Graph) { g.Types[2].Kind = "enum" }, "dedicated Screenplay descriptor"},
		{"event type", func(g *Graph) { g.Types[2].Kind = "event" }, "dedicated Screenplay descriptor"},
		{"projection", func(g *Graph) { g.Types[2].Kind = "projection" }, "dedicated Screenplay descriptor"},
		{"concept", func(g *Graph) { g.Types[2].Fields[0].Type.Contract.Concept = "Price" }, "dedicated Screenplay descriptor"},
		{"nullable collection element", func(g *Graph) { g.Types[1].Fields[2].Type.Element.Nullable = true }, "nullable elements"},
		{"missing collection element", func(g *Graph) { g.Types[1].Fields[2].Type.Element = nil }, "requires an element"},
		{"graph diagnostic", func(g *Graph) { g.Diagnostics = []string{"unsupported"} }, "resolve graph diagnostics"},
		{"empty graph", func(g *Graph) { g.Commands, g.Queries = nil, nil }, "no command or query"},
		{"future version", func(g *Graph) { g.FormatVersion = 999 }, "unsupported graph version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			graph := screenplayGraph()
			tc.change(graph)
			before := screenplaySnapshot(t, graph)
			result, err := exportScreenplayMetadata(graph)
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(result.Document) != 0 {
				t.Fatalf("result = %+v, error = %v; want %q without bytes", result, err, tc.want)
			}
			if !bytes.Equal(before, screenplaySnapshot(t, graph)) {
				t.Fatal("failed export changed input")
			}
		})
	}
}

func TestScreenplayReorderedPackagesProduceIdenticalBytes(t *testing.T) {
	dir := consumer(t)
	for _, pkg := range []string{"commands", "queries"} {
		source, err := os.ReadFile(filepath.Join("testdata", "screenplay", pkg+".go.txt"))
		if err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(dir, pkg, "model.go"), string(source))
	}
	analyses := graphPackages(t, dir, "./commands", "./queries")
	if len(analyses) != 2 {
		t.Fatal("fixture must contribute two packages")
	}
	profile := ApplicationProfile{FormatVersion: ContractGraphVersion, Name: "Tasks"}
	firstGraph, err := buildGraph(analyses, profile, true)
	if err != nil {
		t.Fatal(err)
	}
	before := screenplaySnapshot(t, firstGraph)
	first, err := exportScreenplayMetadata(firstGraph)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(analyses)
	secondGraph, err := buildGraph(analyses, profile, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := exportScreenplayMetadata(secondGraph)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Document, second.Document) || !reflect.DeepEqual(first.Diagnostics, second.Diagnostics) {
		t.Fatal("package enumeration changed export")
	}
	if !bytes.Equal(before, screenplaySnapshot(t, firstGraph)) {
		t.Fatal("later export changed prior graph")
	}
}

func TestScreenplayOmissionsIdentifyAffectedArtifacts(t *testing.T) {
	graph := screenplayGraph()
	graph.Commands[0].ResponseKind = "unknown"
	graph.Commands[0].Roles = []string{"Admin"}
	graph.Queries[0].Paged = true
	graph.Queries[0].Roles = []string{"Reader"}
	graph.Queries[1].Parameters[0].HasDefault = true
	result, err := exportScreenplayMetadata(graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SPG100: command \"Tasks.Register\"", "SPG101: command \"Tasks.Register\"", "SPG101: query \"Tasks.Listing.All\"", "SPG103: query \"Tasks.Listing.All\"", "SPG104: \"Tasks.Listing.Watch\" property \"prefix\""} {
		if !strings.Contains(string(result.Document), want) {
			t.Fatalf("missing omission diagnostic %q", want)
		}
	}
}
