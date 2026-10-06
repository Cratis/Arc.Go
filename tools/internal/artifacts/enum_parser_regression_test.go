// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportedEnumParserRejectsStaleMembersBeforePublication(t *testing.T) {
	dir := consumer(t)
	input := "//arc:namespace Shop\npackage access\n//arc:enum parse=int32\ntype Access int32\nconst (Read Access = 1; Alias Access = 1)\n"
	dependency := filepath.Join(dir, "access", "input.go")
	put(t, dependency, input)
	generate(t, Config{Dir: dir, Patterns: []string{"./access"}})
	parserPath := filepath.Join(dir, "access", Filename)
	parser := string(get(t, parserPath))
	put(t, filepath.Join(dir, "input.go"), "//arc:namespace Shop\npackage consumer\nimport \"example.test/consumer/access\"\n//arc:command\ntype Save struct { Access access.Access }\nfunc (Save) Handle() error { return nil }\n")
	generate(t, Config{Dir: dir, TypeScriptOut: "web"})
	for _, tc := range []struct{ name, source, parser string }{
		{"added_member", input + "const Write Access = 2\n", parser},
		{"renamed_key", input, strings.Replace(parser, `"Read":`, `"Other":`, 1)},
		{"wrong_identifier", input, strings.Replace(parser, `"Read":  Read`, `"Read":  Alias`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "added_member" && tc.parser == parser {
				t.Fatal("parser mutation did not change the fixture")
			}
			put(t, dependency, tc.source)
			put(t, parserPath, tc.parser)
			before := outputInventory(t, dir)
			for _, check := range []bool{false, true} {
				err := Generate(t.Context(), Config{Dir: dir, TypeScriptOut: "web", Check: check})
				if err == nil || !strings.Contains(err.Error(), "imported enum parser for Access is stale; run arc-gen on example.test/consumer/access") {
					t.Fatalf("check=%v admitted stale parser: %v", check, err)
				}
				assertOutputInventory(t, dir, before)
			}
		})
	}
	// Updating a constant's value does not stale the parser: it references the
	// identifier, not a copied number. Alias values remain separate parse names.
	put(t, dependency, strings.Replace(input, "Read Access = 1", "Read Access = 3", 1))
	put(t, parserPath, parser)
	generate(t, Config{Dir: dir, TypeScriptOut: "web"})
	generate(t, Config{Dir: dir, TypeScriptOut: "web", Check: true})
	// Regenerating the dependency repairs an added member for root-only output.
	put(t, dependency, input+"const Write Access = 2\n")
	generate(t, Config{Dir: dir, Patterns: []string{"./access"}})
	generate(t, Config{Dir: dir, TypeScriptOut: "web"})
	generate(t, Config{Dir: dir, TypeScriptOut: "web", Check: true})
}

func TestImportedEnumParserGoOnlyCheckRequiresSelectingEnumPackage(t *testing.T) {
	dir := consumer(t)
	dependency := filepath.Join(dir, "access", "input.go")
	input := "package access\n//arc:enum parse=int32\ntype Access int32\nconst Read Access = 1\n"
	put(t, dependency, input)
	generate(t, Config{Dir: dir, Patterns: []string{"./access"}})
	put(t, filepath.Join(dir, "input.go"), "package consumer\nimport \"example.test/consumer/access\"\n//arc:command\ntype Save struct { Access access.Access }\nfunc (Save) Handle() error { return nil }\n")
	generate(t, Config{Dir: dir})

	put(t, dependency, input+"const Write Access = 2\n")
	before := outputInventory(t, dir)
	// Go-only consumer output has no member list and does not inspect imported
	// parse maps, so neither generation nor a root-only check detects this drift.
	generate(t, Config{Dir: dir})
	generate(t, Config{Dir: dir, Check: true})
	assertOutputInventory(t, dir, before)
	for _, pattern := range []string{"./access", "./..."} {
		t.Run(pattern, func(t *testing.T) {
			err := Generate(t.Context(), Config{Dir: dir, Patterns: []string{pattern}, Check: true})
			if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "access", Filename)+": generated adapters are stale") {
				t.Fatalf("pattern=%s did not detect stale enum output: %v", pattern, err)
			}
			assertOutputInventory(t, dir, before)
		})
	}
	generate(t, Config{Dir: dir, Patterns: []string{"./access"}})
	generate(t, Config{Dir: dir, Patterns: []string{"./..."}, Check: true})
	if parser := get(t, filepath.Join(dir, "access", Filename)); !bytes.Contains(parser, []byte(`"Write": Write`)) {
		t.Fatalf("regeneration did not repair the imported parser:\n%s", parser)
	}
}

func TestEnumOnlyParserDoesNotEmitRegistrationSurface(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), `package consumer
//arc:enum parse=int32
type State int32
const Read State = 1
// These names belong to the application in an enum-only package.
type ArcBindings struct { Value string }
func RegisterArtifacts() string { return "application" }
`)
	generate(t, Config{Dir: dir})
	adapter := get(t, filepath.Join(dir, Filename))
	for _, forbidden := range []string{"type ArcBindings", "func RegisterArtifacts", `"github.com/cratis/arc.go"`} {
		if bytes.Contains(adapter, []byte(forbidden)) {
			t.Fatalf("enum-only package emitted registration surface %q:\n%s", forbidden, adapter)
		}
	}
	if !bytes.Contains(adapter, []byte(`"github.com/cratis/arc.go/serialization"`)) {
		t.Fatal("enum-only parser is missing serialization import")
	}
	generate(t, Config{Dir: dir, Check: true})
	put(t, filepath.Join(dir, "consumer_test.go"), `package consumer
import "testing"
func TestEnumAndApplicationSymbols(t *testing.T) {
 var state State
 if err := state.UnmarshalJSON([]byte("\"Read\"")); err != nil || state != Read { t.Fatalf("state=%v error=%v", state, err) }
 if RegisterArtifacts() != "application" || (ArcBindings{Value:"application"}).Value != "application" { t.Fatal("application symbols changed") }
}
`)
	testEnumConsumer(t, dir)
}

func TestEnumParserOpenAPICommandInputRefusesLocalAndImportedProfiles(t *testing.T) {
	for _, imported := range []bool{false, true} {
		name := "local"
		if imported {
			name = "imported"
		}
		t.Run(name, func(t *testing.T) {
			dir := consumer(t)
			declaration := "//arc:enum parse=int32\ntype State int32\nconst Read State = 1\n"
			source := "//arc:namespace Shop\npackage consumer\n"
			typ := "State"
			testImport := ""
			if imported {
				put(t, filepath.Join(dir, "state", "input.go"), "//arc:namespace Shop\npackage state\n"+declaration)
				generate(t, Config{Dir: dir, Patterns: []string{"./state"}})
				source += "import \"example.test/consumer/state\"\n"
				testImport = "import \"example.test/consumer/state\"\n"
				typ = "state.State"
			} else {
				source += declaration
			}
			source += "//arc:command\ntype Save struct { State " + typ + " }\nfunc (Save) Handle() error { return nil }\n"
			put(t, filepath.Join(dir, "input.go"), source)
			graph, err := buildGraph(graphPackages(t, dir, "."), contractProfile(), false)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, node := range graph.Types {
				if node.Kind == "enum" {
					found = true
					if node.EnumDomain != "int32-parser" {
						t.Fatal("parser projected as ordinary open integer", node)
					}
				}
			}
			if !found {
				t.Fatal("missing command-input enum")
			}
			for _, candidate := range []*Graph{graph, openAPICloneGraph(t, graph)} {
				document, err := renderOpenAPI(candidate)
				if err == nil || !strings.Contains(err.Error(), "Int32 enum parser input semantics") || len(document.bytes()) != 0 {
					t.Fatalf("parser published misleading input schema: %v\n%s", err, document.bytes())
				}
			}
			// Pin the generated runtime behavior responsible for refusing the
			// integer-only schema: strings succeed, undeclared numeric 2 fails.
			generate(t, Config{Dir: dir})
			put(t, filepath.Join(dir, "consumer_test.go"), "package consumer\n"+testImport+`import ("testing"; "encoding/json")
func TestParserInputContract(t *testing.T) {
 var value `+typ+`
 if err := json.Unmarshal([]byte("\"Read\""), &value); err != nil || value != 1 { t.Fatalf("value=%v error=%v", value, err) }
 if err := json.Unmarshal([]byte("2"), &value); err == nil || value != 1 { t.Fatalf("numeric input accepted or mutated receiver: value=%v error=%v", value, err) }
}
`)
			testEnumConsumer(t, dir)
			profile := contractProfile()
			profile.OpenAPI.Out = "openapi.json"
			before := outputInventory(t, dir)
			for _, check := range []bool{false, true} {
				err := Generate(t.Context(), Config{Dir: dir, Profile: &profile, Check: check})
				if err == nil || !strings.Contains(err.Error(), "Int32 enum parser input semantics") {
					t.Fatalf("check=%v published parser OpenAPI: %v", check, err)
				}
				assertOutputInventory(t, dir, before)
			}
		})
	}
}

func testEnumConsumer(t *testing.T, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-count=1", "-timeout=30s", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated enum consumer: %v\n%s", err, output)
	}
}
