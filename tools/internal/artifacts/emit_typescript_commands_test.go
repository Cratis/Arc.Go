// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

func TestTypeScriptCommandFixture(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "Commands")
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), string(get(t, filepath.Join(fixture, "input.go.txt"))))
	graph, err := buildGraph(graphPackages(t, dir, "."), ApplicationProfile{FormatVersion: GraphVersion, Name: "command-fixture"}, true)
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := renderTypeScriptCommands(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 14 {
		t.Fatalf("incomplete command inventory: %d", len(outputs))
	}
	snapshot := filepath.Join(fixture, "Generated")
	var expected []string
	for _, output := range outputs {
		expected = append(expected, filepath.FromSlash(output.path))
		file := filepath.Join(snapshot, filepath.FromSlash(output.path))
		if os.Getenv("ARC_UPDATE_COMMAND_FIXTURE") == "1" {
			put(t, file, string(output.content))
		}
		if !bytes.Equal(get(t, file), output.content) {
			t.Fatalf("stale command fixture %s", file)
		}
	}
	var actual []string
	if err := filepath.WalkDir(snapshot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(snapshot, path)
		if err != nil {
			return err
		}
		actual = append(actual, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("inventory: got %v want %v", actual, expected)
	}
	reversed := *graph
	reversed.Commands = append([]CommandDescriptor(nil), graph.Commands...)
	for l, r := 0, len(reversed.Commands)-1; l < r; l, r = l+1, r-1 {
		reversed.Commands[l], reversed.Commands[r] = reversed.Commands[r], reversed.Commands[l]
	}
	again, err := renderTypeScriptCommands(&reversed)
	if err != nil || !reflect.DeepEqual(outputs, again) {
		t.Fatalf("command order changed output: %v", err)
	}
}

func commandGraph(t *testing.T) *Graph {
	t.Helper()
	declaration := metadata.Command{Type: metadata.TypeName{Namespace: "Shop", Name: "Register"}}
	graph := modelGraph(modelNode("result", "Shop", "Detail", scalarField("label", "string")))
	graph.Catalog = metadata.Catalog{Version: metadata.Version, Commands: []metadata.Command{declaration}}
	graph.Commands = []CommandDescriptor{{Declaration: declaration, TypeKey: "command", ResponseKind: "none", Fields: []FieldDescriptor{scalarField("name", "string")}}}
	var err error
	graph.Endpoints, err = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestCommandPreflightRejectsUnsupportedCompletePlans(t *testing.T) {
	tests := []struct {
		name, message string
		mutate        func(*Graph)
	}{
		{"unknown_response", "finalized response kind", func(g *Graph) { g.Commands[0].ResponseKind = "unknown" }},
		{"missing_response", "response metadata", func(g *Graph) { g.Commands[0].ResponseKind = "value" }},
		{"consumed_response", "no-response", func(g *Graph) { g.Commands[0].Response = &WireType{Kind: "model", Target: "result"} }},
		{"raw_interface", "unsupported wire kind", func(g *Graph) { g.Commands[0].ResponseKind = "value"; g.Commands[0].Response = &WireType{Kind: "any"} }},
		{"nullable_response", "response metadata", func(g *Graph) {
			g.Commands[0].ResponseKind = "value"
			g.Commands[0].Response = &WireType{Kind: "string", Nullable: true}
		}},
		{"dictionary_response", "response metadata", func(g *Graph) {
			g.Commands[0].ResponseKind = "value"
			g.Commands[0].Response = &WireType{Kind: "record"}
		}},
		{"nested_response", "nested/nullable collection", func(g *Graph) {
			g.Commands[0].ResponseKind = "value"
			g.Commands[0].Response = &WireType{Kind: "array", Element: &WireType{Kind: "array"}}
		}},
		{"path_collision", "output collision", func(g *Graph) { g.Types = append(g.Types, modelNode("collision", "Shop", "register")) }},
		{"interface_collision", "barrel export collision", func(g *Graph) { g.Types = append(g.Types, modelNode("collision", "Shop", "IRegister")) }},
		{"runtime_member", "command runtime", func(g *Graph) { g.Commands[0].Fields = append(g.Commands[0].Fields, scalarField("execute", "string")) }},
		{"backing_member", "command runtime", func(g *Graph) { g.Commands[0].Fields = append(g.Commands[0].Fields, scalarField("_value0", "string")) }},
		{"default", "defaults require", func(g *Graph) { g.Commands[0].Fields[0].HasDefault = true }},
		{"format_rule", "unsupported client portable", func(g *Graph) {
			g.Commands[0].Fields[0].Rules = []validation.RuleDescriptor{{Property: "name", Name: "emailAddress", Message: "Email"}}
		}},
		{"server_only", "server-only", func(g *Graph) {
			g.Commands[0].Fields[0].Rules = []validation.RuleDescriptor{{Property: "name", Name: "notEmpty", Message: "Name", ServerOnly: true}}
		}},
		{"endpoint_drift", "endpoints disagree", func(g *Graph) { g.Endpoints[0].Path = "/changed" }},
		{"missing_catalog", "unfinalized", func(g *Graph) { g.Commands[0].Declaration.Path = "/changed" }},
		{"input_used_as_model", "missing or mismatched", func(g *Graph) {
			g.Types = append(g.Types, modelNode("command", "Shop", "Register"))
			g.Types[0].Fields = append(g.Types[0].Fields, FieldDescriptor{Name: "input", Type: WireType{Kind: "model", Target: "command"}})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			graph := commandGraph(t)
			tc.mutate(graph)
			outputs, err := renderTypeScriptCommands(graph)
			if outputs != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("outputs %v, error %v; want %s and nil output", outputs, err, tc.message)
			}
		})
	}
}

func TestCommandRejectsNegativeLayoutSkipBeforePathPlanning(t *testing.T) {
	graph := commandGraph(t)
	skip := -1
	graph.Profile.TypeScript.SegmentsToSkip = &skip
	outputs, err := renderTypeScriptCommands(graph)
	if outputs != nil || err == nil {
		t.Fatalf("outputs %v, error %v; want nil output and invalid-profile error", outputs, err)
	}
}

func TestCommandLayoutAndImportAliases(t *testing.T) {
	graph := commandGraph(t)
	graph.Types[0].Name.Name = "Command"
	graph.Commands[0].ResponseKind = "value"
	graph.Commands[0].Response = &WireType{Kind: "model", Target: "result"}
	skip := 1
	graph.Profile.TypeScript = TypeScriptProfile{SegmentsToSkip: &skip, ProxyFileSuffix: true, NamespaceRoots: []NamespaceRoot{{Namespace: "Shop", Folder: "ui"}}}
	outputs, err := renderTypeScriptCommands(graph)
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for _, output := range outputs {
		if output.path == "ui/Register.proxy.ts" {
			content = string(output.content)
		}
	}
	for _, expected := range []string{"import { Command } from \"./Command.proxy\";", "import { Command as Command_2 }", "extends Command_2<IRegister, Command>", "super(Command, false)", "useCommand<Register, IRegister, Command>"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
}

type portableCommandRules struct {
	Text  *string  `json:"text" rules:"[{\"name\":\"notNull\",\"message\":\"Present\"},{\"name\":\"minLength\",\"arguments\":[2],\"message\":\"Minimum\"},{\"name\":\"maxLength\",\"arguments\":[4],\"message\":\"Maximum\"},{\"name\":\"length\",\"arguments\":[2,4],\"message\":\"Range\"}]"`
	Value float64  `json:"value" rules:"[{\"name\":\"greaterThan\",\"arguments\":[0],\"message\":\"Above\"},{\"name\":\"greaterThanOrEqual\",\"arguments\":[1],\"message\":\"At least\"},{\"name\":\"lessThan\",\"arguments\":[5],\"message\":\"Below\"},{\"name\":\"lessThanOrEqual\",\"arguments\":[4],\"message\":\"At most\"}]"`
	Tags  []string `json:"tags" rules:"[{\"name\":\"notEmpty\",\"message\":\"Tags\"}]"`
}

func TestCommandPortableRulesPairedCorpus(t *testing.T) {
	data := get(t, filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "Commands", "rule-cases.json"))
	var cases []struct {
		Name     string
		Input    portableCommandRules
		Messages []string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 8 {
		t.Fatalf("missing corpus cases: %d", len(cases))
	}
	validator, err := validation.NewPortable[portableCommandRules]()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			results, err := validator.Validate(t.Context(), tc.Input)
			if err != nil {
				t.Fatal(err)
			}
			messages := []string{}
			for _, result := range results {
				messages = append(messages, result.Message)
				if result.Severity != validation.Error {
					t.Fatal(result)
				}
			}
			if !reflect.DeepEqual(messages, tc.Messages) {
				t.Fatalf("got %v want %v", messages, tc.Messages)
			}
		})
	}
}
