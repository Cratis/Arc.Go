// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

func TestTypeScriptQueryFixture(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "Queries")
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), string(get(t, filepath.Join(fixture, "input.go.txt"))))
	profile := ApplicationProfile{FormatVersion: GraphVersion, Name: "snapshot-fixture", ClientHTTP: map[string]string{
		"Shop.Queries.Listing.Find": "Get", "Shop.Queries.Listing.Paged": "Query", "Shop.Queries.Listing.Array": "Auto",
		"Shop.Queries.Listing.GetOnly": "Get", "Shop.Queries.Task.Tasks": "Get", "Shop.Queries.Task.ByID": "Get",
	}}
	graph, err := buildGraph(graphPackages(t, dir, "."), profile, true)
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := renderTypeScriptQueries(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 13 || len(graph.Queries) != 8 {
		t.Fatalf("incomplete snapshot inventory: outputs %d, queries %d", len(outputs), len(graph.Queries))
	}
	snapshot := filepath.Join(fixture, "Generated")
	var expected []string
	for _, output := range outputs {
		expected = append(expected, filepath.FromSlash(output.path))
		file := filepath.Join(snapshot, filepath.FromSlash(output.path))
		if os.Getenv("ARC_UPDATE_QUERY_FIXTURE") == "1" {
			put(t, file, string(output.content))
		}
		if !bytes.Equal(get(t, file), output.content) {
			t.Fatalf("stale query fixture %s", file)
		}
	}
	var actual []string
	if err := filepath.WalkDir(snapshot, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(snapshot, file)
		if err != nil {
			return err
		}
		actual = append(actual, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("inventory got %v want %v", actual, expected)
	}
	reversed := *graph
	reversed.Queries = append([]QueryDescriptor(nil), graph.Queries...)
	reversed.Types = append([]TypeDescriptor(nil), graph.Types...)
	for l, r := 0, len(reversed.Queries)-1; l < r; l, r = l+1, r-1 {
		reversed.Queries[l], reversed.Queries[r] = reversed.Queries[r], reversed.Queries[l]
	}
	for l, r := 0, len(reversed.Types)-1; l < r; l, r = l+1, r-1 {
		reversed.Types[l], reversed.Types[r] = reversed.Types[r], reversed.Types[l]
	}
	again, err := renderTypeScriptQueries(&reversed)
	if err != nil || !reflect.DeepEqual(outputs, again) {
		t.Fatalf("input order changed output: %v", err)
	}
}

func queryGraph(t *testing.T) *Graph {
	t.Helper()
	graph := modelGraph(modelNode("result", "Shop", "Detail", scalarField("name", "string")))
	graph.Types[0].Fields[0].Sortable = true
	declaration := metadata.Query{ReadModel: graph.Types[0].Name, Name: "All"}
	graph.Catalog = metadata.Catalog{Version: metadata.Version, Queries: []metadata.Query{declaration}}
	graph.Queries = []QueryDescriptor{{Declaration: declaration, TypeKey: "result", Result: WireType{Kind: "array", Element: &WireType{Kind: "model", Target: "result"}}, Delivery: "snapshot", SortFields: []string{"name"}, Parameters: []FieldDescriptor{scalarField("id", "Guid")}}}
	var err error
	graph.Endpoints, err = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestQueryPreflightReturnsNoPartialOutput(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		mutate        func(*Graph)
	}{
		{"negative_skip", "segment stripping", func(g *Graph) { skip := -1; g.Profile.TypeScript.SegmentsToSkip = &skip }},
		{"observable", "delivery", func(g *Graph) { g.Queries[0].Delivery = "observable" }},
		{"unknown_result", "wire data", func(g *Graph) { g.Queries[0].Result = WireType{Kind: "any"} }},
		{"opaque_provider", "wire data", func(g *Graph) { g.Queries[0].Result = WireType{Kind: "provider", Target: "result"} }},
		{"wrong_owner", "wire data", func(g *Graph) { g.Queries[0].TypeKey = "other" }},
		{"nullable_element", "collection", func(g *Graph) { g.Queries[0].Result.Element.Nullable = true }},
		{"single_page", "paged", func(g *Graph) { g.Queries[0].Result = *g.Queries[0].Result.Element; g.Queries[0].Paged = true }},
		{"argument_sort", "sortable result", func(g *Graph) { g.Queries[0].SortFields = []string{"id"} }},
		{"duplicate_sort", "unique declared", func(g *Graph) { g.Queries[0].SortFields = []string{"name", "name"} }},
		{"unregistered_sort", "sortable result", func(g *Graph) { g.Types[0].Fields[0].Sortable = false }},
		{"complex_sort", "sortable result", func(g *Graph) {
			g.Types[0].Fields[0].Type = WireType{Kind: "record", Element: &WireType{Kind: "string"}}
		}},
		{"bad_preference", "HTTP preference", func(g *Graph) { g.Queries[0].ClientHTTP = "POST" }},
		{"endpoint_drift", "endpoints disagree", func(g *Graph) { g.Endpoints[0].Path = "/changed" }},
		{"catalog_drift", "unfinalized", func(g *Graph) { g.Queries[0].Declaration.Name = "Changed" }},
		{"missing_declaration", "omits", func(g *Graph) { g.Queries = nil }},
		{"duplicate_query", "duplicate", func(g *Graph) { g.Queries = append(g.Queries, g.Queries[0]) }},
		{"parameter_export_collision", "barrel export collision", func(g *Graph) { g.Types = append(g.Types, modelNode("collision", "Shop", "AllParameters")) }},
		{"model_path_collision", "output collision", func(g *Graph) { g.Types = append(g.Types, modelNode("collision", "Shop", "all")) }},
		{"query_runtime_member", "query runtime", func(g *Graph) { g.Queries[0].Parameters[0].Name = "perform" }},
		{"query_backing_member", "query runtime", func(g *Graph) { g.Queries[0].Parameters[0].Name = "_origin" }},
		{"model_argument", "parameter wire", func(g *Graph) { g.Queries[0].Parameters[0].Type = WireType{Kind: "model", Target: "result"} }},
		{"rich_default", "unsupported server default", func(g *Graph) { g.Queries[0].Parameters[0].HasDefault = true }},
		{"unsafe_default", "unsupported server default", func(g *Graph) {
			g.Queries[0].Parameters[0] = FieldDescriptor{Name: "count", Type: WireType{Kind: "number"}, HasDefault: true, Default: "9007199254740992"}
		}},
		{"required_default", "required and defaulted", func(g *Graph) {
			g.Queries[0].Parameters[0] = FieldDescriptor{Name: "text", Type: WireType{Kind: "string"}, Required: true, HasDefault: true, Default: "server"}
		}},
		{"rules", "paired client semantics", func(g *Graph) { g.Queries[0].Parameters[0].Rules = []validation.RuleDescriptor{{Name: "notNull"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := queryGraph(t)
			tc.mutate(graph)
			outputs, err := renderTypeScriptQueries(graph)
			if outputs != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("outputs %v error %v; want nil and %q", outputs, err, tc.message)
			}
		})
	}
}

func TestQueryHTTPPreferenceCannotPromiseUnexposedMethods(t *testing.T) {
	for _, preference := range []string{"Get", "Auto"} {
		t.Run(preference, func(t *testing.T) {
			graph := queryGraph(t)
			graph.Catalog.Queries[0].HTTPMethod = metadata.QueryHTTPMethod("QUERY")
			graph.Queries[0].Declaration = graph.Catalog.Queries[0]
			graph.Queries[0].ClientHTTP = preference
			var err error
			graph.Endpoints, err = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
			if err != nil {
				t.Fatal(err)
			}
			outputs, err := renderTypeScriptQueries(graph)
			if outputs != nil || err == nil || !strings.Contains(err.Error(), "incompatible") {
				t.Fatalf("outputs %v, error %v", outputs, err)
			}
		})
	}
}

func TestQueryPrimitiveDefaultsAreOmittedAndRequirednessIsIndependentOfNullability(t *testing.T) {
	graph := queryGraph(t)
	graph.Queries[0].Parameters = []FieldDescriptor{
		{Name: "id", Type: WireType{Kind: "Guid", Nullable: true}, Required: true},
		{Name: "limit", Type: WireType{Kind: "number"}, HasDefault: true, Default: "10"},
		{Name: "enabled", Type: WireType{Kind: "boolean"}, HasDefault: true, Default: "false"},
		{Name: "text", Type: WireType{Kind: "string"}, HasDefault: true, Default: "server"},
	}
	outputs, err := renderTypeScriptQueries(graph)
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for _, output := range outputs {
		if strings.HasSuffix(output.path, "/All.ts") {
			content = string(output.content)
		}
	}
	for _, expected := range []string{"id: Guid;", "limit?: number;", "enabled?: boolean;", "text?: string;", "return [\"id\"]", "id?: Guid;", "limit!: number;"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
	if strings.Contains(content, "this.limit =") || strings.Contains(content, "this.enabled =") || strings.Contains(content, "this.text =") {
		t.Fatal("server defaults assigned on proxy")
	}
}

func TestQueryFamilyKeepsCoordinatedCommandBytes(t *testing.T) {
	graph := queryGraph(t)
	command := metadata.Command{Type: metadata.TypeName{Namespace: "Shop", Name: "Register"}}
	graph.Catalog.Commands = []metadata.Command{command}
	graph.Commands = []CommandDescriptor{{Declaration: command, TypeKey: "command", ResponseKind: "none", Fields: []FieldDescriptor{scalarField("text", "string")}}}
	var err error
	graph.Endpoints, err = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		t.Fatal(err)
	}
	before, err := renderTypeScriptCommands(graph)
	if err != nil {
		t.Fatal(err)
	}
	after, err := renderTypeScriptQueries(graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range before {
		if filepath.Base(output.path) == "index.ts" {
			continue
		}
		found := false
		for _, candidate := range after {
			if candidate.path == output.path {
				found = bytes.Equal(candidate.content, output.content)
			}
		}
		if !found {
			t.Fatalf("coordinated query rendering changed %s", output.path)
		}
	}
}

func TestQueryLayoutAliasesAndNoAutomaticPaging(t *testing.T) {
	graph := queryGraph(t)
	graph.Types[0].Name.Name = "QueryFor"
	graph.Catalog.Queries[0].ReadModel = graph.Types[0].Name
	graph.Queries[0].Declaration = graph.Catalog.Queries[0]
	skip := 1
	graph.Profile.TypeScript = TypeScriptProfile{SegmentsToSkip: &skip, ProxyFileSuffix: true, NamespaceRoots: []NamespaceRoot{{Namespace: "Shop", Folder: "ui"}}}
	var err error
	graph.Endpoints, err = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := renderTypeScriptQueries(graph)
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for _, output := range outputs {
		if output.path == "ui/All.proxy.ts" {
			content = string(output.content)
		}
	}
	for _, expected := range []string{"import { QueryFor } from \"./QueryFor.proxy\"", "QueryFor as QueryFor_2", "extends QueryFor_2<QueryFor[], AllParameters>", "super(QueryFor, true)", "new SortingActionsForQuery<QueryFor[]>(\"name\", query)", "new Paging(0, pageSize)"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
	if strings.Contains(content, "this.paging =") || strings.Contains(content, "this.setHttpMethod") {
		t.Fatal("plain list gained paging or unspecified HTTP override", content)
	}
}
