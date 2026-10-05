// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const queryStringConceptCodecs = `
func (TextValue) ConceptValue() string { panic("analysis executed concept") }
func (TextValue) MarshalJSON() ([]byte, error) { panic("analysis executed codec") }
func (*TextValue) UnmarshalJSON([]byte) error { panic("analysis executed codec") }
func (TextValue) MarshalText() ([]byte, error) { panic("analysis executed codec") }
func (*TextValue) UnmarshalText([]byte) error { panic("analysis executed codec") }
`

func queryRepresentationSource(declaration, fieldType, rule string) string {
	tag := ""
	if rule != "" {
		rules := `[{"name":"notEmpty","message":"Text is required"}]`
		if rule == "minLength" {
			rules = `[{"name":"minLength","arguments":[3],"message":"Three units"}]`
		}
		tag = " rules:" + fmt.Sprintf("%q", rules)
	}
	return "//arc:namespace Shop.Validation\npackage consumer\n" + declaration + `
//arc:readmodel
//arc:allow-anonymous
type Match struct { Text string ` + "`json:\"text\"`" + ` }
type SearchArguments struct { Text ` + fieldType + " `json:\"text\"" + tag + "` }\n" + `
func (Match) Search(SearchArguments) ([]Match, error) { return nil, nil }
`
}

func queryRepresentationProfile(version int, schemas bool) ApplicationProfile {
	profile := ApplicationProfile{FormatVersion: version, Name: "query-representation", TypeScript: TypeScriptProfile{Out: "web"}}
	if version == ContractGraphVersion && schemas {
		profile.WireSchemas = map[string]WireSchemas{"example.test/consumer.TextValue": {Input: json.RawMessage(`{"type":"string"}`), Output: json.RawMessage(`{"type":"string"}`)}}
	}
	return profile
}

func TestQueryRuleRepresentationSurvivesBothGraphFormats(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, fieldType string
		want                         QueryRuleRepresentation
		admitted                     bool
	}{
		{"string", "", "*string", QueryRuleRepresentation{GoKind: "string", PointerDepth: 1}, true},
		{"named string", "type TextValue string", "*TextValue", QueryRuleRepresentation{GoKind: "string", PointerDepth: 1}, true},
		{"struct concept", "type TextValue struct { Value string }" + queryStringConceptCodecs, "*TextValue", QueryRuleRepresentation{GoKind: "struct", PointerDepth: 1, CustomCodec: true}, false},
		{"string concept codec", "type TextValue string" + queryStringConceptCodecs, "*TextValue", QueryRuleRepresentation{GoKind: "string", PointerDepth: 1, CustomCodec: true}, false},
		{"nested pointers", "", "**string", QueryRuleRepresentation{GoKind: "string", PointerDepth: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "input.go"), queryRepresentationSource(tc.declaration, tc.fieldType, "minLength"))
			loaded := graphPackages(t, dir, ".")
			for _, version := range []int{GraphVersion, ContractGraphVersion} {
				t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
					graph, err := freshContractGraph(t, loaded, queryRepresentationProfile(version, tc.want.CustomCodec), true)
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(graph)
					if err != nil {
						t.Fatal(err)
					}
					var restored Graph
					if err := json.Unmarshal(encoded, &restored); err != nil {
						t.Fatal(err)
					}
					if got := restored.Queries[0].Parameters[0].QueryRules; got == nil || !reflect.DeepEqual(*got, tc.want) {
						t.Fatalf("representation=%+v want=%+v", got, tc.want)
					}
					outputs, err := renderTypeScriptQueries(&restored)
					if tc.admitted {
						if err != nil || len(outputs) == 0 {
							t.Fatalf("proven string refused: %v", err)
						}
					} else if outputs != nil || err == nil || !strings.Contains(err.Error(), "proven Go string representation") {
						t.Fatalf("unproved representation admitted: outputs=%v error=%v", outputs, err)
					}
					after, err := json.Marshal(&restored)
					if err != nil || !bytes.Equal(encoded, after) {
						t.Fatal("rendering mutated serialized graph", err)
					}
				})
			}
		})
	}
}

func TestQueryRuleRepresentationNamedStringRegistersAndExecutes(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "QueryValidation")
	for _, version := range []int{GraphVersion, ContractGraphVersion} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			dir := consumer(t)
			source := string(get(t, filepath.Join(fixture, "input.go.txt")))
			source = strings.Replace(source, "type SearchArguments struct", "type PlainString string\n\ntype SearchArguments struct", 1)
			source = strings.Replace(source, "Text *string", "Text *PlainString", 1)
			source = strings.Replace(source, "Text: *args.Text", "Text: string(*args.Text)", 1)
			put(t, filepath.Join(dir, "input.go"), source)
			profile := queryRepresentationProfile(version, false)
			generate(t, Config{Dir: dir, Profile: &profile})
			put(t, filepath.Join(dir, "contract_test.go"), string(get(t, filepath.Join(fixture, "contract_test.go.txt"))))
			put(t, filepath.Join(dir, "cases.json"), string(get(t, filepath.Join(fixture, "cases.json"))))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=25s", "./...")
			command.Dir = dir
			command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("named string registration/runtime consumer: %v\n%s", err, output)
			}
		})
	}
}

func TestQueryRuleRepresentationProductionCLIRefusesBeforePublication(t *testing.T) {
	binary := buildArcGenCLI(t)
	for _, version := range []int{GraphVersion, ContractGraphVersion} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			dir := consumer(t)
			profile := queryRepresentationProfile(version, false)
			writeProfile := func(profile ApplicationProfile) {
				t.Helper()
				encoded, err := json.Marshal(profile)
				if err != nil {
					t.Fatal(err)
				}
				put(t, filepath.Join(dir, "profile.json"), string(encoded))
			}
			cli := func(check bool) ([]byte, error) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
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
			writeProfile(profile)
			put(t, filepath.Join(dir, "input.go"), queryRepresentationSource("", "*string", "notEmpty"))
			if output, err := cli(false); err != nil {
				t.Fatalf("initial publication: %v\n%s", err, output)
			}
			for _, tc := range []struct{ name, declaration, fieldType, rule string }{
				{"struct length", "type TextValue struct { Value string }" + queryStringConceptCodecs, "*TextValue", "minLength"},
				{"struct whitespace", "type TextValue struct { Value string }" + queryStringConceptCodecs, "*TextValue", "notEmpty"},
				{"string codec", "type TextValue string" + queryStringConceptCodecs, "*TextValue", "notEmpty"},
				{"nested pointers", "", "**string", "notEmpty"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					writeProfile(queryRepresentationProfile(version, tc.fieldType == "*TextValue"))
					put(t, filepath.Join(dir, "input.go"), queryRepresentationSource(tc.declaration, tc.fieldType, tc.rule))
					before := outputInventory(t, dir)
					for _, check := range []bool{false, true} {
						if output, err := cli(check); err == nil || !bytes.Contains(output, []byte("proven Go string representation")) {
							t.Fatalf("publication/check=%v accepted unproved representation: %v\n%s", check, err, output)
						}
						assertOutputInventory(t, dir, before)
					}
				})
			}
			// The representation restriction is rule-specific, not a ban on wire
			// string concepts. Preserve the established rule-free publication.
			writeProfile(queryRepresentationProfile(version, true))
			put(t, filepath.Join(dir, "input.go"), queryRepresentationSource("type TextValue struct { Value string }"+queryStringConceptCodecs, "*TextValue", ""))
			if output, err := cli(false); err != nil {
				t.Fatalf("rule-free concept publication: %v\n%s", err, output)
			}
			if output, err := cli(true); err != nil {
				t.Fatalf("rule-free concept check: %v\n%s", err, output)
			}
		})
	}
}
