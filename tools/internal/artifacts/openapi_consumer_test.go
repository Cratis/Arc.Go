//go:build ignore

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type openAPIProofLoader struct{ calls *int }

func (loader openAPIProofLoader) Load(uri string) (any, error) {
	*loader.calls++
	return nil, fmt.Errorf("external loading forbidden: %s", uri)
}

// This explicitly selected, offline test uses the retained alternate tools
// manifest and a test-only Go overlay replacing this file's ignore constraint.
// The ignore constraint also excludes proof dependencies from go mod tidy.
// Neither dependency is required by ordinary tools builds. Kin is a
// disposable standard consumer view only: never reserialize or use its numeric
// instance validator. This is not the later generated-client/HTTP parity gate.
func TestOpenAPIRendererOfficialSchemaAndConsumer(t *testing.T) {
	graph := openAPIFixture(t)
	document, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	original := document.bytes()
	raw, err := os.ReadFile("testdata/openapi/official-3.1-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 33483 || fmt.Sprintf("%x", sha256.Sum256(raw)) != "59f106413cb48c31299f96f024c938d3628aed6cd02cd14bcfb2fcaae7a130b6" {
		t.Fatal("official schema pin changed")
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	loads := 0
	compiler.UseLoader(openAPIProofLoader{calls: &loads})
	const officialID = "https://spec.openapis.org/oas/3.1/schema/2026-08-03"
	const documentID = "https://renderer.invalid/document.json"
	decode := func(data []byte) any {
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if err := compiler.AddResource(officialID, decode(raw)); err != nil {
		t.Fatal(err)
	}
	official, err := compiler.Compile(officialID)
	if err != nil {
		t.Fatal(err)
	}
	exact := decode(original)
	if err := official.Validate(exact); err != nil {
		t.Fatal("renderer output violates pinned official structure", err)
	}
	if err := compiler.AddResource(documentID, exact); err != nil {
		t.Fatal(err)
	}
	components := exact.(map[string]any)["components"].(map[string]any)["schemas"].(map[string]any)
	compiled := map[string]*jsonschema.Schema{}
	for _, name := range sortedKeys(components) {
		schema, err := compiler.Compile(documentID + "#/components/schemas/" + name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		compiled[name] = schema
	}
	if len(compiled) != 17 {
		t.Fatal("unexpected schema inventory", len(compiled))
	}
	for _, tc := range []struct {
		name, component, instance string
		valid                     bool
	}{
		{"zero binding", "Model.Shop.Save.Input", `{}`, true},
		{"exact extrema", "Model.Shop.Save.Input", `{"signed":-9223372036854775808,"unsigned":18446744073709551615}`, true},
		{"signed maximum", "Model.Shop.Save.Input", `{"signed":9223372036854775807}`, true},
		{"below signed", "Model.Shop.Save.Input", `{"signed":-9223372036854775809}`, false},
		{"above signed", "Model.Shop.Save.Input", `{"signed":9223372036854775808}`, false},
		{"above unsigned", "Model.Shop.Save.Input", `{"unsigned":18446744073709551616}`, false},
		{"negative unsigned", "Model.Shop.Save.Input", `{"unsigned":-1}`, false},
		{"fraction", "Model.Shop.Save.Input", `{"signed":1.5}`, false},
		{"pointer null", "Model.Shop.Save.Input", `{"maybe":null}`, true},
		{"optional null", "Model.Shop.Save.Input", `{"choice":null}`, true},
		{"wrong fixed length", "Model.Shop.Save.Input", `{"fixed":[1]}`, false},
		{"null element", "Model.Shop.Save.Input", `{"nodes":[null,{"value":1,"next":null}]}`, true},
		{"recursive model", "Model.Shop.Node.Output", `{"value":1,"next":{"value":2}}`, true},
		{"missing required output", "Model.Shop.Node.Output", `{}`, false},
		{"omitted pointer is not null", "Model.Shop.Node.Output", `{"value":1,"next":null}`, false},
		{"enum open", "Model.Shop.State.Input", `42`, true},
		{"enum exact maximum", "Model.Shop.State.Output", `18446744073709551615`, true},
		{"enum above maximum", "Model.Shop.State.Output", `18446744073709551616`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compiled[tc.component].Validate(decode([]byte(tc.instance)))
			if (err == nil) != tc.valid {
				t.Fatalf("%s => %v, want valid=%t", tc.instance, err, tc.valid)
			}
		})
	}
	// Foundation expectations are explicitly source-derived (not a C# capture).
	// These independent bytes must validate against actual emitted envelopes.
	fixtures, err := os.ReadFile("../../../ContractTests/fixtures/v1/envelopes.json")
	if err != nil {
		t.Fatal(err)
	}
	envelopes := decode(fixtures).(map[string]any)
	for _, name := range []string{"command-success", "command-zero", "command-denied", "command-validation", "command-malformed", "command-exception"} {
		body := envelopes[name].(map[string]any)["body"]
		if err := compiled["Operation.Shop.Save.Execute"].Validate(body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	zero := envelopes["command-zero"].(map[string]any)["body"]
	if err := compiled["Operation.Shop.Save.Validate"].Validate(zero); err == nil {
		t.Fatal("validation result admitted an execution payload")
	}
	if err := compiled["Cratis.ValidationResult"].Validate(envelopes["validation-state"].(map[string]any)["body"]); err == nil {
		t.Fatal("absent-state profile admitted an opaque state")
	}
	failed := envelopes["command-denied"].(map[string]any)["body"].(map[string]any)
	failed["response"] = 1
	if err := compiled["Operation.Shop.Save.Execute"].Validate(failed); err == nil {
		t.Fatal("failed command accepted an execution payload")
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	consumer, err := loader.LoadFromData(original)
	if err != nil {
		t.Fatal("standard structural consumer rejected renderer output", err)
	}
	if err := consumer.Validate(t.Context(), openapi3.DisableSchemaDefaultsValidation(), openapi3.DisableExamplesValidation()); err != nil {
		t.Fatal("standard structural consumer validation", err)
	}
	if consumer.OpenAPI != "3.1.1" || consumer.Paths.Len() != 5 || consumer.Paths.Value("/api/shop/all").Get == nil || consumer.Paths.Value("/api/shop/all").Head == nil {
		t.Fatal("consumer lost ordinary operations")
	}
	if loads != 0 || !bytes.Equal(document.bytes(), original) {
		t.Fatal("validation attempted I/O or changed authoritative bytes", loads)
	}
}
