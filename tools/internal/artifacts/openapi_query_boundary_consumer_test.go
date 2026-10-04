//go:build ignore

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Selected with the retained alternate tools manifest and the unchanged Stage 1
// overlay (package/embed paths only). Compile the actual request pointer in the
// whole rendered document, never a reconstructed standalone sorting schema.
func TestOpenAPIQuerySortingExactSchemaWitnesses(t *testing.T) {
	request := openAPIQueryBoundarySchema(t)
	for _, tc := range openAPIQuerySortingWitnesses() {
		t.Run(tc.name, func(t *testing.T) {
			instance, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			err = request.Validate(instance)
			// Unrecognized string directions are pipeline validation failures,
			// not malformed JSON types; the request schema admits them.
			if (err == nil) != !tc.malformed {
				t.Fatalf("exact request %s: %v; want valid=%t", tc.body, err, !tc.malformed)
			}
		})
	}
}

func TestOpenAPIQueryIntegerTokenExactSchemaWitnesses(t *testing.T) {
	request := openAPIQueryBoundarySchema(t)
	for _, tc := range openAPIQueryIntegerWitnesses() {
		t.Run(tc.body, func(t *testing.T) {
			instance, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			// JSON Schema integer is mathematical: these are all exactly one.
			// The paired reader test deliberately rejects decimal/exponent tokens.
			if err := request.Validate(instance); err != nil {
				t.Fatalf("mathematical integer %s must validate: %v", tc.body, err)
			}
		})
	}
}

func openAPIQueryBoundarySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	document, err := renderOpenAPI(openAPIQueryFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	original := document.bytes()
	loads := 0
	structure, overlay, err := officialStructure(&loads)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := officialCheck(original, structure, overlay, &loads, true)
	if err != nil {
		t.Fatal(err)
	}
	const pointer = "/paths/~1api~1plain/x-cratis-query/operation/requestBody/content/application~1json/schema"
	request := checked.compiled[pointer]
	if request == nil || len(checked.inventory.operations) != 4 {
		t.Fatal("missing actual request pointer or changed operation inventory", checked.inventory)
	}
	if loads != 0 || !bytes.Equal(original, document.bytes()) {
		t.Fatal("external loading or authoritative-byte mutation", loads)
	}
	return request
}
