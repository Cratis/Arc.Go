package capability_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const documentJSON = `{
  "openapi":"3.1.1",
  "info":{"title":"Capability gate","version":"1"},
  "paths":{},
  "components":{"schemas":{
    "Nullable":{"type":["string","null"]},
    "Wrapper":{"type":"object","required":["value"],"properties":{"value":{"$ref":"#/components/schemas/Nullable"}}},
    "Signed":{"type":"integer","minimum":-9223372036854775808,"maximum":9223372036854775807},
    "Unsigned":{"type":"integer","minimum":0,"maximum":18446744073709551615}
  }}
}`

func loadDocument(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.Context = t.Context()
	loader.IsExternalRefsAllowed = false
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, uri *url.URL) ([]byte, error) {
		t.Errorf("unexpected URI read: %s", uri)
		return nil, fmt.Errorf("URI reads disabled: %s", uri)
	}
	document, err := loader.LoadFromData([]byte(documentJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestOpenAPI31NullLocalReferenceAndDeterminism(t *testing.T) {
	document := loadDocument(t)
	schema := document.Components.Schemas["Wrapper"].Value
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"null", map[string]any{"value": nil}, true},
		{"string", map[string]any{"value": "hello"}, true},
		{"wrong_type", map[string]any{"value": true}, false},
		{"missing", map[string]any{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := schema.VisitJSON(tc.value, openapi3.EnableJSONSchema2020())
			if (err == nil) != tc.valid {
				t.Errorf("valid = %t, want %t; error = %v", err == nil, tc.valid, err)
			}
		})
	}
	first, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("serializations differ")
	}
	loader := openapi3.NewLoader()
	roundTrip, err := loader.LoadFromData(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := roundTrip.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestExact64BitSchemaExtrema(t *testing.T) {
	document := loadDocument(t)
	for _, tc := range []struct {
		name    string
		keyword string
		want    string
	}{
		{"Signed", "minimum", "-9223372036854775808"},
		{"Signed", "maximum", "9223372036854775807"},
		{"Unsigned", "maximum", "18446744073709551615"},
	} {
		t.Run(tc.name+"_"+tc.keyword, func(t *testing.T) {
			encoded, err := json.Marshal(document.Components.Schemas[tc.name].Value)
			if err != nil {
				t.Fatal(err)
			}
			var keywords map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &keywords); err != nil {
				t.Fatal(err)
			}
			gotText := string(keywords[tc.keyword])
			got, ok := new(big.Rat).SetString(gotText)
			if !ok {
				t.Fatalf("invalid number %q", gotText)
			}
			want, ok := new(big.Rat).SetString(tc.want)
			if !ok {
				t.Fatal("invalid expectation")
			}
			if got.Cmp(want) != 0 {
				t.Errorf("%s.%s serialized as %s, want exact %s", tc.name, tc.keyword, gotText, tc.want)
			}
		})
	}
}

func TestExact64BitInstanceBoundaries(t *testing.T) {
	document := loadDocument(t)
	for _, tc := range []struct {
		name   string
		schema string
		value  string
		valid  bool
	}{
		{"signed_min", "Signed", "-9223372036854775808", true},
		{"below_signed_min", "Signed", "-9223372036854775809", false},
		{"signed_max", "Signed", "9223372036854775807", true},
		{"above_signed_max", "Signed", "9223372036854775808", false},
		{"unsigned_zero", "Unsigned", "0", true},
		{"below_unsigned_min", "Unsigned", "-1", false},
		{"unsigned_max", "Unsigned", "18446744073709551615", true},
		{"above_unsigned_max", "Unsigned", "18446744073709551616", false},
		{"fractional", "Unsigned", "0.5", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := document.Components.Schemas[tc.schema].Value.VisitJSON(json.Number(tc.value), openapi3.EnableJSONSchema2020())
			if (err == nil) != tc.valid {
				t.Errorf("%s instance %s: valid = %t, want %t; error = %v", tc.schema, tc.value, err == nil, tc.valid, err)
			}
		})
	}
}
