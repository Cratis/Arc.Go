// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

type openAPITraversalPayload struct{ Count int }

func (openAPITraversalPayload) MarshalJSONWith(func(any) ([]byte, error)) ([]byte, error) {
	return []byte(`"opaque"`), nil
}

// The renderer cannot inspect go/types or guess hooks from struct fields. Shared
// graph analysis must classify traversal hooks as opaque before ordinary models;
// the existing declared-schema seam then remains a whole-document refusal here.
func TestOpenAPIRendererRefusesTraversalHook(t *testing.T) {
	data, err := serialization.Marshal(openAPITraversalPayload{Count: 42})
	if err != nil || string(data) != `"opaque"` {
		t.Fatalf("runtime traversal-hook witness: %s / %v", data, err)
	}
	source := `type Payload struct { Count int }
func (Payload) MarshalJSONWith(func(any) ([]byte,error)) ([]byte,error) { return []byte("\"opaque\""), nil }
//arc:command
type Save struct { Value Payload }
func (Save) Handle() error { return nil }
`
	loaded := contractPackages(t, source)
	if _, err := freshContractGraph(t, loaded, contractProfile(), false); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("opaque traversal hook must require a shared-graph schema declaration, got %v", err)
	}
	profile := contractProfile()
	profile.WireSchemas = map[string]WireSchemas{"example.test/consumer.Payload": {
		Input:  json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`),
		Output: json.RawMessage(`{"type":"string"}`),
	}}
	graph, err := freshContractGraph(t, loaded, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	document, err := renderOpenAPI(graph)
	if err == nil || len(document.bytes()) != 0 || !strings.Contains(err.Error(), "opaque") {
		t.Fatalf("declared custom traversal codec must refuse the whole document: %s / %v", document.bytes(), err)
	}
}
