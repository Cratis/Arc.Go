// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Source-derived contract witnesses, NOT a captured C# document or a parity
// claim. Arc 7c1e78075b737df64f69fddfaae83374f75e3612:
// Source/DotNET/OpenApi/ModelBound/CommandOperationTransformer.cs (required JSON
// body, typed command result), QueryOperationTransformer.cs (query envelope),
// Arc.Core/JsonSerializerOptionsConfiguration.cs (camelCase and null omission).
// Go directional presence, open enum domain and exact widths come from the
// existing shared Graph, not from the C# coarse OpenAPI 3.0 document.
func openAPIFixture(t *testing.T) *Graph {
	t.Helper()
	graph, err := contractGraph(t, `import (
 "github.com/cratis/arc.go/commands"
 "github.com/cratis/arc.go/serialization"
)
//arc:enum
type State uint64
const (Pending State = 0; Complete State = 18446744073709551615)
type Node struct { Value int64; Next *Node }
//arc:command
type Save struct { Signed int64; Unsigned uint64; Narrow int8; State State; Maybe *int; Choice serialization.Optional[int]; Nodes []*Node; Fixed [2]int16; Values map[string]*int }
func (Save) Handle() (commands.Outcome[int64], error) { return commands.Respond(int64(0)), nil }
//arc:command
type Clear struct { Enabled bool }
func (Clear) Handle() error { return nil }
//arc:readmodel
type Row struct { ID string; Count uint64; Label *string }
//arc:query http=GET
func (Row) All() ([]Row, error) { return nil, nil }
`, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func openAPIDecode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func openAPICloneGraph(t *testing.T, graph *Graph) *Graph {
	t.Helper()
	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	var result Graph
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func TestOpenAPIRendererExactGraphProjection(t *testing.T) {
	graph := openAPIFixture(t)
	document, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	decoded := openAPIDecode(t, document.bytes())
	if decoded["openapi"] != "3.1.1" || decoded["jsonSchemaDialect"] != openAPIDialect {
		t.Fatal(decoded)
	}
	components := decoded["components"].(map[string]any)["schemas"].(map[string]any)
	input := components["Model.Shop.Save.Input"].(map[string]any)
	output := components["Model.Shop.Save.Output"].(map[string]any)
	if _, exists := input["required"]; exists {
		t.Fatal("zero-bound command fields are not JSON-required", input)
	}
	properties := input["properties"].(map[string]any)
	for _, tc := range []struct{ field, key, value string }{
		{"signed", "minimum", "-9223372036854775808"},
		{"signed", "maximum", "9223372036854775807"},
		{"unsigned", "maximum", "18446744073709551615"},
		{"narrow", "minimum", "-128"},
	} {
		got := properties[tc.field].(map[string]any)[tc.key]
		if got != json.Number(tc.value) {
			t.Fatalf("%s.%s = %v (%T)", tc.field, tc.key, got, got)
		}
	}
	if properties["maybe"].(map[string]any)["anyOf"] == nil || output["properties"].(map[string]any)["maybe"].(map[string]any)["anyOf"] != nil {
		t.Fatal("input null acceptance and output nil-property omission were conflated")
	}
	if output["properties"].(map[string]any)["choice"].(map[string]any)["anyOf"] == nil {
		t.Fatal("Optional explicit null disappeared")
	}
	enum := components["Model.Shop.State.Input"].(map[string]any)
	if enum["enum"] != nil || enum["maximum"] != json.Number("18446744073709551615") {
		t.Fatal("enum closed its open underlying numeric domain", enum)
	}
	paths := decoded["paths"].(map[string]any)
	if len(paths) != 5 {
		t.Fatal("wrong route inventory", paths)
	}
	post := paths["/api/shop/save"].(map[string]any)["post"].(map[string]any)
	if post["operationId"] != "Execute.Shop.Save.POST" || post["requestBody"].(map[string]any)["required"] != true {
		t.Fatal(post)
	}
	validation := components["Operation.Shop.Save.Validate"].(map[string]any)["properties"].(map[string]any)
	if validation["response"] != nil {
		t.Fatal("validation operation acquired execution response")
	}
	query := paths["/api/shop/all"].(map[string]any)
	if query["get"] == nil || query["head"] == nil || query["post"] != nil || query["query"] != nil {
		t.Fatal(query)
	}
	for _, response := range query["head"].(map[string]any)["responses"].(map[string]any) {
		if response.(map[string]any)["content"] != nil {
			t.Fatal("HEAD advertises a response body")
		}
	}
}

func TestOpenAPIRendererOwnershipAndOrdering(t *testing.T) {
	graph := openAPIFixture(t)
	before, _ := json.Marshal(graph)
	original, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	data := original.bytes()
	data[0] = '!'
	if original.bytes()[0] != '{' {
		t.Fatal("document exposed owned bytes")
	}
	reordered := openAPICloneGraph(t, graph)
	slices.Reverse(reordered.Types)
	slices.Reverse(reordered.Commands)
	slices.Reverse(reordered.Queries)
	slices.Reverse(reordered.Endpoints)
	slices.Reverse(reordered.Framework)
	slices.Reverse(reordered.Catalog.Commands)
	for i := range reordered.Types {
		slices.Reverse(reordered.Types[i].Fields)
		slices.Reverse(reordered.Types[i].Members)
	}
	changed, err := renderOpenAPI(reordered)
	if err != nil || !bytes.Equal(original.bytes(), changed.bytes()) {
		t.Fatalf("reordering/serialized Graph changed output: %v", err)
	}
	after, _ := json.Marshal(graph)
	if !bytes.Equal(before, after) {
		t.Fatal("renderer mutated graph")
	}
	graph.Profile.OpenAPI.Title = "changed later"
	if bytes.Contains(original.bytes(), []byte("changed later")) {
		t.Fatal("document retains caller state")
	}
}

func TestOpenAPIRendererRefusesWholeDocument(t *testing.T) {
	graph := openAPIFixture(t)
	for _, tc := range []struct {
		name, message string
		mutate        func(*Graph)
	}{
		{"v1", "finalized v2", func(g *Graph) { g.FormatVersion = GraphVersion }},
		{"missing assertions", "explicit OpenAPI/server", func(g *Graph) { g.Assertions = nil }},
		{"framework endpoints", "framework routes", func(g *Graph) { g.Profile.OpenAPI.IncludeFrameworkEndpoints = true }},
		{"stream metadata", "streaming metadata", func(g *Graph) { g.Profile.OpenAPI.Streaming = "metadata" }},
		{"diagnostic", "diagnostics", func(g *Graph) { g.Diagnostics = []string{"unknown codec"} }},
		{"server URI", "server URL", func(g *Graph) { g.Profile.OpenAPI.Servers = []string{"https://example.test"} }},
		{"duplicate node", "duplicate or empty type", func(g *Graph) { g.Types = append(g.Types, g.Types[0]) }},
		{"malformed enum scalar", "open integer domain", func(g *Graph) {
			for i := range g.Types {
				if g.Types[i].Kind == "enum" {
					g.Types[i].Scalar.Representation = "string"
				}
			}
		}},
		{"rounded bound", "width/signedness", func(g *Graph) {
			for i := range g.Types {
				if g.Types[i].Kind == "enum" {
					g.Types[i].Scalar.Maximum = "18446744073709552000"
				}
			}
		}},
		{"duplicate endpoint", "endpoint inventory", func(g *Graph) { g.Endpoints = append(g.Endpoints, g.Endpoints[0]) }},
		{"duplicate command", "duplicate or inconsistent command", func(g *Graph) { g.Commands = append(g.Commands, g.Commands[0]) }},
		{"dangling input", "unresolved type", func(g *Graph) { g.Commands[0].TypeKey = "missing"; g.Commands[0].Input.Target = "missing" }},
		{"missing framework", "framework inventory", func(g *Graph) { g.Framework = nil }},
		{"paged single result", "collection result", func(g *Graph) { g.Queries[0].Paged = true; g.Queries[0].Result = *g.Queries[0].Result.Element }},
		{"stream", "snapshot", func(g *Graph) { g.Queries[0].Delivery = "observable" }},
		{"schema injection", "declared codecs", func(g *Graph) {
			g.Types[0].Fields[0].Type.Contract.Schemas = &WireSchemas{Input: json.RawMessage(`{"$ref":"https://invalid.test"}`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := openAPICloneGraph(t, graph)
			tc.mutate(candidate)
			before, _ := json.Marshal(candidate)
			document, err := renderOpenAPI(candidate)
			if err == nil || !strings.Contains(err.Error(), tc.message) || len(document.bytes()) != 0 {
				t.Fatalf("got document=%s error=%v; want %q", document.bytes(), err, tc.message)
			}
			after, _ := json.Marshal(candidate)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal mutated graph")
			}
			slices.Reverse(candidate.Types)
			slices.Reverse(candidate.Commands)
			slices.Reverse(candidate.Endpoints)
			_, reorderedError := renderOpenAPI(candidate)
			if reorderedError == nil || reorderedError.Error() != err.Error() {
				t.Fatalf("reordering changed refusal: %v / %v", err, reorderedError)
			}
		})
	}
}

func TestOpenAPIRendererRefusesActualUnsupportedGraphs(t *testing.T) {
	for _, tc := range []struct{ name, source, message string }{
		{"query float argument", "//arc:readmodel\ntype Row struct { ID string }; type Args struct { Ratio float64 }; func (Row) All(Args) ([]Row, error) { return nil, nil }", "special-value codec"},
		{"float codec", "//arc:command\ntype Save struct { Number float64 }; func (Save) Handle() error { return nil }", "special-value codec"},
		{"date codec", "import \"time\"\n//arc:command\ntype Save struct { Date time.Time }; func (Save) Handle() error { return nil }", "unsupported wire kind"},
		{"rules", "//arc:command\ntype Save struct { Name string `validate:\"required\"` }; func (Save) Handle() error { return nil }", "validation rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := contractGraph(t, tc.source, contractProfile(), false)
			if err != nil {
				t.Fatal(err)
			}
			document, err := renderOpenAPI(graph)
			if err == nil || !strings.Contains(err.Error(), tc.message) || len(document.bytes()) > 0 {
				t.Fatalf("error=%v bytes=%s", err, document.bytes())
			}
		})
	}
}

func TestOpenAPIComponentNamesAreInjective(t *testing.T) {
	seen := map[string]bool{}
	for _, identity := range []string{"Shop.A_B", "Shop.A_5FB", "Shop.Å", "Shop._C3_85", "Shop.A-B", "Shop.A.B"} {
		name := openAPIName(identity)
		if seen[name] {
			t.Fatal("component collision", identity, name)
		}
		seen[name] = true
	}
}

func TestOpenAPIIntegerAdmissionHasNoFloatRoundTrip(t *testing.T) {
	for _, value := range []string{"-9223372036854775808", "9223372036854775807", "18446744073709551615"} {
		number, err := openAPIInteger(value)
		data, marshalErr := json.Marshal(number)
		if err != nil || marshalErr != nil || string(data) != value {
			t.Fatalf("%s => %s (%v, %v)", value, data, err, marshalErr)
		}
	}
	for _, value := range []string{"+1", "01", "-0", "1.0", "1e3", "NaN", "", "1\n"} {
		if _, err := openAPIInteger(value); err == nil {
			t.Fatal("noncanonical integer admitted", value)
		}
	}
	if !reflect.DeepEqual(openAPIRef("Model.Shop.Save.Input"), openAPIObject{"$ref": "#/components/schemas/Model.Shop.Save.Input"}) {
		t.Fatal("local ref changed")
	}
}
