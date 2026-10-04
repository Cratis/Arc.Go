// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package capability_test

import (
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// x- names in maps are not Specification Extensions. These fixtures cover the
// relevant OAS 3.1.1 fixed-field map contracts, separately from extensible
// Paths, Responses and Callback Objects. The pinned schema remains unchanged.
const officialXNamedFixture = `{
 "openapi":"3.1.1","jsonSchemaDialect":"https://json-schema.org/draft/2020-12/schema","info":{"title":"x-named entries","version":"1"},
 "components":{
  "schemas":{"x-schema":{"type":"string","$defs":{"x-def":{"type":"string"}},"properties":{"x-property":{"type":"string"}},"patternProperties":{"x-pattern":{"type":"string"}},"dependentSchemas":{"x-dependent":{"type":"string"}}}},
  "parameters":{"x-parameter":{"name":"q","in":"query","schema":{"type":"string"}}},
  "headers":{"x-header":{"schema":{"type":"string"}}},
  "requestBodies":{"x-body":{"content":{"application/json":{"schema":{"type":"string"}}}}},
  "responses":{"x-response":{"description":"","content":{"application/json":{"schema":{"type":"string"}}}}},
  "examples":{"x-example":{"value":"ok"},"x-data":{"value":{}}},
  "links":{"x-link":{"operationId":"selected"}},
  "callbacks":{"x-callback":{"{$request.body#/url}":{"post":{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]}}}},
  "pathItems":{"x-path":{"get":{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]}}},
  "securitySchemes":{"x-key":{"type":"apiKey","in":"header","name":"x-key"}}
 },
 "security":[{"x-key":[]}],
 "webhooks":{"x-hook":{"post":{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]}}},
 "paths":{}
}`

const officialXNamedOperation = `{
 "operationId":"selected",
 "parameters":[{"name":"q","in":"query","schema":{"type":"string"},"examples":{"x-example":{"$ref":"#/components/examples/x-example"}}}],
 "responses":{"200":{"description":"",
  "headers":{"x-test":{"schema":{"$ref":"#/components/schemas/x-schema"},"examples":{"x-example":{"$ref":"#/components/examples/x-example"}}}},
  "links":{"x-link":{"$ref":"#/components/links/x-link"}},
  "content":{"application/json":{"schema":{"type":"string"},"examples":{"x-example":{"$ref":"#/components/examples/x-example"}},"encoding":{"x-field":{"headers":{"x-test":{"schema":{"type":"string"}}}}}}}
 }},
 "callbacks":{"x-callback":{"$ref":"#/components/callbacks/x-callback"}}
}`

func officialXNamedData(t *testing.T, isQuery bool) ([]byte, string) {
	t.Helper()
	document, err := decodeExactDocument([]byte(officialXNamedFixture))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := jsonschema.UnmarshalJSON(strings.NewReader(officialXNamedOperation))
	if err != nil {
		t.Fatal(err)
	}
	pointer := "/paths/~1native/get"
	item := map[string]any{"get": operation}
	path := "/native"
	if isQuery {
		pointer = "/paths/~1query/x-cratis-query/operation"
		item = map[string]any{"x-cratis-query": map[string]any{"method": "QUERY", "operation": operation}}
		path = "/query"
	}
	officialObject(document.value["paths"])[path] = item
	data, err := document.bytes()
	if err != nil {
		t.Fatal(err)
	}
	return data, pointer
}

func officialReplaceAt(t *testing.T, data []byte, pointer string, value any) []byte {
	t.Helper()
	document, err := decodeExactDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	separator := strings.LastIndex(pointer, "/")
	token := pointer[separator+1:]
	container, err := officialAt(document.value, pointer[:separator])
	if err != nil {
		t.Fatal(err)
	}
	object := officialObject(container)
	if object == nil {
		t.Fatalf("replacement parent is not an object: %s", pointer)
	}
	object[strings.NewReplacer("~1", "/", "~0", "~").Replace(token)] = value
	result, err := document.bytes()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestOfficialCapabilityXNamedMapEntries(t *testing.T) {
	structure, query, calls := officialHarness(t)
	for _, isQuery := range []bool{false, true} {
		label := "native"
		if isQuery {
			label = "QUERY"
		}
		t.Run(label, func(t *testing.T) {
			data, operation := officialXNamedData(t, isQuery)
			media := operation + "/responses/200/content/application~1json"
			headers := operation + "/responses/200/headers/x-test"
			schemas := []string{
				"/components/schemas/x-schema", "/components/schemas/x-schema/$defs/x-def", "/components/schemas/x-schema/properties/x-property", "/components/schemas/x-schema/patternProperties/x-pattern", "/components/schemas/x-schema/dependentSchemas/x-dependent",
				"/components/parameters/x-parameter/schema", "/components/headers/x-header/schema", "/components/requestBodies/x-body/content/application~1json/schema", "/components/responses/x-response/content/application~1json/schema",
				"/components/callbacks/x-callback/{$request.body#~1url}/post/parameters/0/schema", "/components/pathItems/x-path/get/parameters/0/schema", "/webhooks/x-hook/post/parameters/0/schema",
				operation + "/parameters/0/schema", headers + "/schema", media + "/schema", media + "/encoding/x-field/headers/x-test/schema",
			}
			slices.Sort(schemas)
			t.Run("inventory_and_instances", func(t *testing.T) {
				result := officialAssertCase(t, data, true, structure, query, calls)
				if !reflect.DeepEqual(result.inventory.schemas, schemas) {
					t.Fatalf("x-named fixture schemas = %v, want %v", result.inventory.schemas, schemas)
				}
				for _, pointer := range schemas {
					compiled := result.compiled[pointer]
					location, err := url.Parse(compiled.Location)
					if err != nil || location.Fragment != pointer || strings.Split(location.String(), "#")[0] != exactResource {
						t.Fatalf("original pointer lost: %s -> %s", pointer, compiled.Location)
					}
					if err := compiled.Validate("ok"); err != nil {
						t.Fatalf("string rejected at %s: %v", pointer, err)
					}
					if err := compiled.Validate(false); err == nil {
						t.Fatalf("non-string accepted at %s", pointer)
					}
				}
			})
			for _, pointer := range schemas {
				t.Run(pointer, func(t *testing.T) {
					for _, tc := range []struct {
						name  string
						value any
					}{
						{"non_schema", "not a schema"},
						{"malformed_type", map[string]any{"type": "not-a-type"}},
						{"unsupported_keyword", map[string]any{"unknownAssertion": true}},
						{"invalid_default", map[string]any{"type": "string", "default": false}},
						{"https_ref", map[string]any{"$ref": "https://outside.invalid/schema.json#/x"}},
						{"file_ref", map[string]any{"$ref": "file:///outside.json#/x"}},
						{"relative_ref", map[string]any{"$ref": "outside.json#/x"}},
					} {
						t.Run(tc.name, func(t *testing.T) {
							officialAssertCase(t, officialReplaceAt(t, data, pointer, tc.value), false, structure, query, calls)
						})
					}
				})
			}
			for _, tc := range []struct{ pointer, kind, target string }{
				{headers, "header", "/components/headers/x-header"},
				{media + "/encoding/x-field/headers/x-test", "header", "/components/headers/x-header"},
				{operation + "/responses/200/links/x-link", "link", "/components/links/x-link"},
				{operation + "/callbacks/x-callback", "callback", "/components/callbacks/x-callback"},
				{operation + "/parameters/0/examples/x-example", "example", "/components/examples/x-example"},
				{headers + "/examples/x-example", "example", "/components/examples/x-example"},
				{media + "/examples/x-example", "example", "/components/examples/x-example"},
				{"/webhooks/x-hook", "path-item", "/components/pathItems/x-path"},
			} {
				t.Run(tc.pointer+"_reference", func(t *testing.T) {
					t.Run("local_x_named_target", func(t *testing.T) {
						result := officialAssertCase(t, officialReplaceAt(t, data, tc.pointer, map[string]any{"$ref": "#" + tc.target}), true, structure, query, calls)
						if result.inventory.objects[tc.pointer] != tc.kind || result.inventory.refs[tc.pointer] != tc.target {
							t.Fatalf("typed reference absent at %s", tc.pointer)
						}
					})
					for _, ref := range []string{"https://outside.invalid/document.json#/x", "file:///outside.json#/x", "outside.json#/x", "#/missing", "#/components/schemas/x-schema"} {
						t.Run(ref, func(t *testing.T) {
							officialAssertCase(t, officialReplaceAt(t, data, tc.pointer, map[string]any{"$ref": ref}), false, structure, query, calls)
						})
					}
				})
			}
			t.Run("x_content_key_is_not_an_extension", func(t *testing.T) {
				value := map[string]any{"x-custom": map[string]any{"schema": true}}
				officialAssertCase(t, officialReplaceAt(t, data, operation+"/responses/200/content", value), false, structure, query, calls)
			})
		})
	}
}

func TestOfficialCapabilityExtensionsRemainOpaque(t *testing.T) {
	structure, query, calls := officialHarness(t)
	data, operation := officialXNamedData(t, false)
	baseline := officialAssertCase(t, data, true, structure, query, calls)
	// Schema-looking keys in genuine extensions and example values are data.
	opaque := map[string]any{"schema": map[string]any{"unknownAssertion": true}, "$ref": "https://outside.invalid/not-a-reference"}
	for _, pointer := range []string{
		"/paths/x-note", "/components/x-note", operation + "/x-note", operation + "/responses/x-note", operation + "/responses/200/x-note", operation + "/responses/200/headers/x-test/x-note",
		"/components/callbacks/x-callback/x-note", "/components/examples/x-data/value",
	} {
		t.Run(pointer, func(t *testing.T) {
			value := any(opaque)
			if pointer == "/components/callbacks/x-callback/x-note" {
				// The informative schema also applies its Path Item shape to
				// callback extension values; use that shape without traversing it.
				value = map[string]any{"$ref": "https://outside.invalid/not-a-reference"}
			}
			changed := officialReplaceAt(t, data, pointer, value)
			result := officialAssertCase(t, changed, true, structure, query, calls)
			if !reflect.DeepEqual(result.inventory, baseline.inventory) {
				t.Fatal("extension/example data changed the typed inventory")
			}
		})
	}
}
