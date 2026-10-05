// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

func TestQueryValidationProductionConsumer(t *testing.T) {
	binary := buildArcGenCLI(t)
	fixture := filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "QueryValidation")
	dir := consumer(t)
	source := string(get(t, filepath.Join(fixture, "input.go.txt")))
	put(t, filepath.Join(dir, "input.go"), source)
	put(t, filepath.Join(dir, "profile.json"), `{"formatVersion":1,"name":"query-validation","typescript":{"out":"web"}}`)
	cli := func(check bool) ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		args := []string{"-dir", dir, "-config", filepath.Join(dir, "profile.json")}
		if check {
			args = append(args, "-check")
		}
		command := exec.CommandContext(ctx, binary, append(args, ".")...)
		command.Dir = "../.."
		command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
		return command.CombinedOutput()
	}
	if output, err := cli(false); err != nil {
		t.Fatalf("production generation: %v\n%s", err, output)
	}
	if output, err := cli(true); err != nil {
		t.Fatalf("production check: %v\n%s", err, output)
	}
	adapter := get(t, filepath.Join(dir, Filename))
	for _, expected := range []string{"NewPortable[SearchArguments]", "WithValidator[SearchArguments]", "ExpectGeneratedEndpoints"} {
		if !bytes.Contains(adapter, []byte(expected)) {
			t.Fatalf("missing %s in adapter", expected)
		}
	}
	// These tracked fixtures are bytes from the production CLI, not a handwritten
	// validator. Frontend tests compile and execute exactly this complete inventory.
	var inventory []string
	if err := filepath.WalkDir(filepath.Join(dir, "web"), func(file string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(filepath.Join(dir, "web"), file)
		if err != nil {
			return err
		}
		inventory = append(inventory, filepath.ToSlash(relative))
		expected := filepath.Join(fixture, "Generated", relative)
		if os.Getenv("ARC_UPDATE_QUERY_VALIDATION_FIXTURE") == "1" {
			put(t, expected, string(get(t, file)))
		}
		if !bytes.Equal(get(t, file), get(t, expected)) {
			t.Fatalf("stale production fixture: %s", relative)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	expected := []string{manifestName, "Shop/Validation/Match.ts", "Shop/Validation/Search.ts", "Shop/Validation/index.ts"}
	if !reflect.DeepEqual(inventory, expected) {
		t.Fatalf("inventory %v want %v", inventory, expected)
	}
	put(t, filepath.Join(dir, "contract_test.go"), string(get(t, filepath.Join(fixture, "contract_test.go.txt"))))
	put(t, filepath.Join(dir, "cases.json"), string(get(t, filepath.Join(fixture, "cases.json"))))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("independent runtime consumer: %v\n%s", err, output)
	}
	// An unproved format rule must refuse the entire publication, including all
	// already owned adapters, proxies, manifests and journals.
	put(t, filepath.Join(dir, "input.go"), strings.Replace(source, `\"name\":\"notEmpty\"`, `\"name\":\"emailAddress\"`, 1))
	before := outputInventory(t, dir)
	if output, err := cli(false); err == nil || !bytes.Contains(output, []byte("unsupported client portable rule")) {
		t.Fatalf("unsupported rule was not refused: %v\n%s", err, output)
	}
	assertOutputInventory(t, dir, before)
	observed := strings.Replace(source, "package consumer", "package consumer\nimport \"github.com/cratis/arc.go/observable\"", 1)
	observed += "\nfunc (Match) Watch(args SearchArguments) (observable.Source[Match], error) { return nil, nil }\n"
	put(t, filepath.Join(dir, "input.go"), observed)
	before = outputInventory(t, dir)
	if output, err := cli(false); err == nil || !bytes.Contains(output, []byte("observable query validation")) {
		t.Fatalf("observable rules were not refused: %v\n%s", err, output)
	}
	assertOutputInventory(t, dir, before)
}

func queryValidationGraph(t *testing.T) *Graph {
	t.Helper()
	graph := queryGraph(t)
	graph.Queries[0].PortableRules = true
	graph.Queries[0].Parameters = []FieldDescriptor{{Name: "text", Type: WireType{Kind: "string", Nullable: true}, QueryRules: &QueryRuleRepresentation{GoKind: "string", PointerDepth: 1}, Rules: []validation.RuleDescriptor{{Property: "text", Name: "minLength", Arguments: []json.RawMessage{json.RawMessage("3")}, Message: "Three units"}}}}
	return graph
}

func TestQueryValidationRefusesUnprovedContractsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		mutate        func(*Graph)
	}{
		{"observable", "observable query validation", func(g *Graph) {
			g.Queries[0].Delivery = "observable"
			g.Queries[0].Declaration.Observable = true
			g.Catalog.Queries[0].Observable = true
			var err error
			g.Endpoints, err = metadata.Resolve(g.Catalog, g.Profile.routeOptions())
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"number", "scalar strings", func(g *Graph) { g.Queries[0].Parameters[0].Type.Kind = "number" }},
		{"missing representation", "proven Go string representation", func(g *Graph) { g.Queries[0].Parameters[0].QueryRules = nil }},
		{"struct representation", "proven Go string representation", func(g *Graph) { g.Queries[0].Parameters[0].QueryRules.GoKind = "struct" }},
		{"custom codec", "proven Go string representation", func(g *Graph) { g.Queries[0].Parameters[0].QueryRules.CustomCodec = true }},
		{"nested pointers", "proven Go string representation", func(g *Graph) { g.Queries[0].Parameters[0].QueryRules.PointerDepth = 2 }},
		{"format", "unsupported client portable rule", func(g *Graph) { g.Queries[0].Parameters[0].Rules[0].Name = "emailAddress" }},
		{"optional rule", "optional/concept", func(g *Graph) { g.Queries[0].Parameters[0].Rules[0].Optional = true }},
		{"concept rule", "optional/concept", func(g *Graph) { g.Queries[0].Parameters[0].Rules[0].Concept = true }},
		{"server rule", "server-only", func(g *Graph) { g.Queries[0].Parameters[0].Rules[0].ServerOnly = true }},
		{"member drift", "unsupported", func(g *Graph) { g.Queries[0].Parameters[0].Rules[0].Property = "Text" }},
		{"message placeholder", "message substitution", func(g *Graph) { g.Queries[0].Parameters[0].Rules[0].Message = "{PropertyName}" }},
		{"optional Go zero", "required or nullable", func(g *Graph) { g.Queries[0].Parameters[0].Type.Nullable = false }},
		{"server default", "without server defaults", func(g *Graph) { g.Queries[0].Parameters[0].HasDefault = true }},
		{"presence-preserving binding", "empty/null-as-missing", func(g *Graph) {
			g.Queries[0].Parameters[0].Binding = &QueryBinding{Reader: "builtin", PreservePresence: true}
		}},
		{"custom binding", "empty/null-as-missing", func(g *Graph) {
			g.Queries[0].Parameters[0].Binding = &QueryBinding{Reader: "custom", EmptyAsMissing: true, NullAsMissing: true}
		}},
		{"invalid severity", "static portable contract", func(g *Graph) {
			severity := validation.Severity(4)
			g.Queries[0].Parameters[0].Rules[0].Severity = &severity
		}},
		{"missing portable flag", "metadata disagrees", func(g *Graph) { g.Queries[0].PortableRules = false }},
		{"missing rules", "metadata disagrees", func(g *Graph) { g.Queries[0].Parameters[0].Rules = nil }},
		{"validator export", "barrel export collision", func(g *Graph) { g.Types = append(g.Types, modelNode("collision", "Shop", "AllValidator")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := queryValidationGraph(t)
			tc.mutate(graph)
			before, err := json.Marshal(graph)
			if err != nil {
				t.Fatal(err)
			}
			outputs, err := renderTypeScriptQueries(graph)
			if outputs != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("outputs=%v error=%v want %q", outputs, err, tc.message)
			}
			after, err := json.Marshal(graph)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refused rendering mutated graph", err)
			}
		})
	}
}

func TestQueryValidationAliasesRuntimeNameAndReservesValidator(t *testing.T) {
	graph := queryValidationGraph(t)
	graph.Types[0].Name.Name = "QueryValidator"
	graph.Catalog.Queries[0].ReadModel = graph.Types[0].Name
	graph.Queries[0].Declaration = graph.Catalog.Queries[0]
	var err error
	graph.Endpoints, err = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := renderTypeScriptQueries(graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range outputs {
		if strings.HasSuffix(output.path, "/All.ts") {
			for _, expected := range []string{"QueryValidator as QueryValidator_2", "AllValidator extends QueryValidator_2<AllParameters>", "readonly validation = new AllValidator()", `this.ruleFor(c => c["text"]).minLength(3).withMessage("Three units")`} {
				if !bytes.Contains(output.content, []byte(expected)) {
					t.Fatalf("missing %q in %s", expected, output.content)
				}
			}
			return
		}
	}
	t.Fatal("query output missing")
}
