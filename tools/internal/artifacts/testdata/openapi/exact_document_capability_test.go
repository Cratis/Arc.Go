package capability_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const exactDialect = "https://json-schema.org/draft/2020-12/schema"
const exactResource = "https://capability.invalid/openapi.json"

// OpenAPIDocument is a test-only, copy-owned exact JSON DTO, NOT openapi3.T.
// Neither validator's mutable view is the publication representation.
// This failed capability fixture is not a production validator or renderer.
type OpenAPIDocument struct {
	value map[string]any
}

func decodeExactDocument(data []byte) (OpenAPIDocument, error) {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(bytes.Clone(data)))
	if err != nil {
		return OpenAPIDocument{}, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return OpenAPIDocument{}, fmt.Errorf("document must be an object")
	}
	return OpenAPIDocument{value: object}, nil
}

func (d OpenAPIDocument) bytes() ([]byte, error) { return json.Marshal(d.value) }

func decimalNumber(text string) (json.Number, error) {
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(text))
	if err != nil {
		return "", err
	}
	number, ok := value.(json.Number)
	if !ok {
		return "", fmt.Errorf("not a JSON number: %q", text)
	}
	return number, nil
}

const exactFixtureJSON = `{
 "openapi":"3.1.1", "jsonSchemaDialect":"https://json-schema.org/draft/2020-12/schema",
 "info":{"title":"Exact capability only","version":"1"},
 "components":{"schemas":{
  "Signed":{"type":"integer","minimum":-9223372036854775808,"maximum":9223372036854775807,"default":9223372036854775807,"examples":[-9223372036854775808,9223372036854775807]},
  "Unsigned":{"type":"integer","minimum":0,"maximum":18446744073709551615,"example":18446744073709551615},
  "Nullable":{"type":["string","null"]},
  "Wrapper":{"type":"object","required":["value"],"properties":{"value":{"$ref":"#/components/schemas/Nullable"}}},
  "Recursive":{"type":"object","properties":{"next":{"anyOf":[{"type":"null"},{"$ref":"#/components/schemas/Recursive"}]}}},
  "Small":{"type":"integer","minimum":-128,"maximum":127},
  "Exclusive":{"type":"number","exclusiveMinimum":9223372036854775807,"exclusiveMaximum":9223372036854775809},
  "Multiple":{"type":"number","multipleOf":0.1},
  "Constant":{"const":18446744073709551615},
  "Enumeration":{"enum":[-9223372036854775808,18446744073709551615]}
 }},
 "paths":{
  "/native":{"get":{"operationId":"native","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Unsigned"}}}}}}},
  "/query":{"x-cratis-query":{"method":"QUERY","operation":{"operationId":"query","responses":{"200":{"description":"inline","content":{"application/json":{"schema":{"type":"integer","minimum":0,"maximum":18446744073709551615}}}}}}}}
 }
}`

// Explicit inventory of EVERY actual Schema Object in this immutable fixture,
// including unreferenced components and nested/local-ref schemas. This is not
// schema discovery for arbitrary input. The failing structural proof stops that
// larger implementation; nothing is stripped, extracted, rebased or replaced.
var exactSchemaPointers = []string{
	"/components/schemas/Signed", "/components/schemas/Unsigned",
	"/components/schemas/Nullable", "/components/schemas/Wrapper",
	"/components/schemas/Wrapper/properties/value",
	"/components/schemas/Recursive", "/components/schemas/Recursive/properties/next",
	"/components/schemas/Recursive/properties/next/anyOf/0",
	"/components/schemas/Recursive/properties/next/anyOf/1",
	"/components/schemas/Small", "/components/schemas/Exclusive",
	"/components/schemas/Multiple", "/components/schemas/Constant", "/components/schemas/Enumeration",
	"/paths/~1native/get/responses/200/content/application~1json/schema",
	"/paths/~1query/x-cratis-query/operation/responses/200/content/application~1json/schema",
}

type refusingLoader struct{ calls *int }

func (loader refusingLoader) Load(uri string) (any, error) {
	(*loader.calls)++
	return nil, fmt.Errorf("resource I/O disabled: %s", uri)
}

func compileExactFixture(data []byte, calls *int) (map[string]*jsonschema.Schema, error) {
	document, err := decodeExactDocument(data)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(refusingLoader{calls})
	// The SAME WHOLE exact document, under one fixed absolute synthetic URI.
	if err := compiler.AddResource(exactResource, document.value); err != nil {
		return nil, err
	}
	compiled := make(map[string]*jsonschema.Schema, len(exactSchemaPointers))
	for _, pointer := range exactSchemaPointers {
		schema, err := compiler.Compile(exactResource + "#" + pointer)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", pointer, err)
		}
		compiled[pointer] = schema
	}
	return compiled, nil
}

func structuralFixture(t *testing.T, data []byte, calls *int) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.Context = t.Context()
	loader.IsExternalRefsAllowed = false
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, uri *url.URL) ([]byte, error) {
		(*calls)++
		return nil, fmt.Errorf("kin URI I/O disabled: %s", uri)
	}
	document, err := loader.LoadFromData(bytes.Clone(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(t.Context(), openapi3.DisableExamplesValidation(), openapi3.DisableSchemaDefaultsValidation()); err != nil {
		t.Fatal(err)
	}
	return document // disposable structural view only; NEVER serialize it
}

func TestExactDocumentCapabilityNumbers(t *testing.T) {
	document, err := decodeExactDocument([]byte(exactFixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	schemas := document.value["components"].(map[string]any)["schemas"].(map[string]any)
	for _, tc := range []struct{ schema, keyword, decimal string }{
		{"Signed", "minimum", "-9223372036854775808"}, {"Signed", "maximum", "9223372036854775807"},
		{"Unsigned", "minimum", "0"}, {"Unsigned", "maximum", "18446744073709551615"},
	} {
		// Bounds are json.Number DIRECT from validated decimal strings.
		number, err := decimalNumber(tc.decimal)
		if err != nil {
			t.Fatal(err)
		}
		schemas[tc.schema].(map[string]any)[tc.keyword] = number
	}
	data, err := document.bytes()
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(data)
	calls := 0
	compiled, err := compileExactFixture(data, &calls)
	if err != nil {
		t.Fatal(err)
	}
	structuralFixture(t, data, &calls)
	if calls != 0 || !bytes.Equal(before, data) {
		t.Fatalf("I/O calls = %d or authoritative bytes changed", calls)
	}
	t.Logf("compiled %d actual Schema Objects at original document pointers", len(compiled))
	decoded, err := decodeExactDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	physicalSchemas := decoded.value["components"].(map[string]any)["schemas"].(map[string]any)
	for _, tc := range []struct{ schema, keyword, want string }{
		{"Signed", "minimum", "-9223372036854775808"}, {"Signed", "maximum", "9223372036854775807"},
		{"Unsigned", "minimum", "0"}, {"Unsigned", "maximum", "18446744073709551615"},
	} {
		t.Run(tc.schema+"_"+tc.keyword, func(t *testing.T) {
			got := physicalSchemas[tc.schema].(map[string]any)[tc.keyword]
			number, ok := got.(json.Number)
			if !ok {
				t.Fatalf("physical JSON numeric token decoded as %T", got)
			}
			rat, ok := new(big.Rat).SetString(string(number))
			want, valid := new(big.Rat).SetString(tc.want)
			if !ok || !valid || rat.Cmp(want) != 0 {
				t.Fatalf("physical numeric bound %s != %s", number, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name, schema, instance string
		valid                  bool
	}{
		{"signed_min", "Signed", "-9223372036854775808", true}, {"signed_max", "Signed", "9223372036854775807", true},
		{"unsigned_zero", "Unsigned", "0", true}, {"unsigned_max", "Unsigned", "18446744073709551615", true},
		{"below_signed_min", "Signed", "-9223372036854775809", false}, {"above_signed_max", "Signed", "9223372036854775808", false},
		{"above_unsigned_max", "Unsigned", "18446744073709551616", false}, {"negative_unsigned", "Unsigned", "-1", false},
		{"fractional", "Unsigned", "0.5", false},
		{"small_min", "Small", "-128", true}, {"small_max", "Small", "127", true},
		{"small_below", "Small", "-129", false}, {"small_above", "Small", "128", false},
		{"exclusive_inside", "Exclusive", "9223372036854775808", true},
		{"exclusive_lower", "Exclusive", "9223372036854775807", false}, {"exclusive_upper", "Exclusive", "9223372036854775809", false},
		{"multiple_exact", "Multiple", "0.3", true}, {"multiple_fraction", "Multiple", "0.31", false},
		{"const_exact", "Constant", "18446744073709551615", true}, {"const_adjacent", "Constant", "18446744073709551616", false},
		{"enum_exact", "Enumeration", "18446744073709551615", true}, {"enum_adjacent", "Enumeration", "18446744073709551616", false},
		{"nullable_null", "Wrapper", `{"value":null}`, true}, {"nullable_string", "Wrapper", `{"value":"hello"}`, true},
		{"nullable_wrong", "Wrapper", `{"value":true}`, false}, {"nullable_missing", "Wrapper", `{}`, false},
		{"recursive_valid", "Recursive", `{"next":{"next":null}}`, true}, {"recursive_invalid", "Recursive", `{"next":{"next":12}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.instance))
			if err != nil {
				t.Fatal(err)
			}
			err = compiled["/components/schemas/"+tc.schema].Validate(instance)
			if (err == nil) != tc.valid {
				t.Fatalf("exact instance %s: valid = %t, want %t; %v", tc.instance, err == nil, tc.valid, err)
			}
		})
	}
	for _, pointer := range exactSchemaPointers {
		if compiled[pointer].Location != exactResource+"#"+pointer {
			t.Fatalf("schema not compiled at original pointer: %s", pointer)
		}
	}
	for _, pointer := range exactSchemaPointers[len(exactSchemaPointers)-2:] {
		if err := compiled[pointer].Validate(json.Number("18446744073709551615")); err != nil {
			t.Fatal(err)
		}
		if err := compiled[pointer].Validate(json.Number("18446744073709551616")); err == nil {
			t.Fatalf("inline/local-ref schema accepted invalid number at %s", pointer)
		}
	}
	for _, tc := range []struct{ schema, keyword string }{{"Signed", "default"}, {"Unsigned", "example"}} {
		value := physicalSchemas[tc.schema].(map[string]any)[tc.keyword]
		if err := compiled["/components/schemas/"+tc.schema].Validate(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, example := range physicalSchemas["Signed"].(map[string]any)["examples"].([]any) {
		if err := compiled["/components/schemas/Signed"].Validate(example); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExactDocumentCapabilityOwnership(t *testing.T) {
	input := []byte(exactFixtureJSON)
	document, err := decodeExactDocument(input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := document.bytes()
	if err != nil {
		t.Fatal(err)
	}
	input[0] = '!'
	second, err := document.bytes()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("DTO retained caller bytes")
	}
	first[0] = '!'
	third, err := document.bytes()
	if err != nil || !bytes.Equal(second, third) {
		t.Fatal("DTO returned shared bytes")
	}
	roundTrip, err := decodeExactDocument(third)
	if err != nil {
		t.Fatal(err)
	}
	fourth, err := roundTrip.bytes()
	if err != nil || !bytes.Equal(third, fourth) || !reflect.DeepEqual(document.value, roundTrip.value) {
		t.Fatal("exact decode/encode not deterministic")
	}
}

// This assertion MUST remain failing at kin v0.149.0. The capability gate
// requires an actual invalid QUERY Operation to be refused, not post-filtered.
// No fake POST, native 3.2 operation, patched cache or general validator is used.
func TestExactDocumentCapabilityQueryRejectsInvalidResponseKey(t *testing.T) {
	document, err := decodeExactDocument([]byte(exactFixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	query := document.value["paths"].(map[string]any)["/query"].(map[string]any)["x-cratis-query"].(map[string]any)
	operation := query["operation"].(map[string]any)
	operation["responses"] = map[string]any{"wrong": map[string]any{"description": "invalid response key"}}
	data, err := document.bytes()
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(data)
	calls := 0
	structuralFixture(t, data, &calls)
	// Decode independently from the SAME authoritative bytes, not kin's view.
	exact, err := decodeExactDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	actualQuery := exact.value["paths"].(map[string]any)["/query"].(map[string]any)["x-cratis-query"].(map[string]any)
	encoded, err := json.Marshal(actualQuery["operation"])
	if err != nil {
		t.Fatal(err)
	}
	var actualOperation openapi3.Operation
	if err := json.Unmarshal(encoded, &actualOperation); err != nil {
		t.Fatal(err)
	}
	validationErr := actualOperation.Validate(t.Context(), openapi3.IsOpenAPI31OrLater(), openapi3.DisableExamplesValidation(), openapi3.DisableSchemaDefaultsValidation())
	if calls != 0 || !bytes.Equal(before, data) {
		t.Fatalf("I/O calls = %d or authoritative bytes changed", calls)
	}
	if validationErr == nil {
		t.Fatal("kin v0.149.0 actual QUERY Operation.Validate accepted response key \"wrong\"; Stage 1 capability BLOCKED")
	}
}
