// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const mixedCodecOperations = `
//arc:command
type AOrdinary struct { Enabled bool }
func (AOrdinary) Handle() error { return nil }
//arc:command
type ZSave struct { Payload *Opaque }
func (ZSave) Handle() error { return nil }
`

func TestOpenAPICodecDeclaredProvenanceRefusesWholeDocumentAfterRoundTrip(t *testing.T) {
	// The ordinary structural control is in the renderer's admitted profile.
	ordinary, err := contractGraph(t, "type Opaque struct { Exported string }"+mixedCodecOperations, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	if document, err := renderOpenAPI(ordinary); err != nil || len(document.bytes()) == 0 {
		t.Fatalf("ordinary structural control refused: %v", err)
	}

	profile := contractProfile()
	schemas := WireSchemas{Input: json.RawMessage(`{"type":"string","minLength":1}`), Output: json.RawMessage(`{"type":"string"}`)}
	profile.WireSchemas = map[string]WireSchemas{"example.test/consumer.Opaque": schemas}
	graph, err := contractGraph(t, "type Opaque struct { Exported string }"+strings.ReplaceAll(queryStringConceptCodecs, "TextValue", "Opaque")+traversalCodecPanicMethod+mixedCodecOperations, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Commands) != 2 || graph.Assertions == nil || graph.Assertions.Provenance != "application-profile-assertion" {
		t.Fatal("mixed operation/provenance inventory missing", graph)
	}
	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	var restored Graph
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*Graph{graph, &restored} {
		var declared *TypeDescriptor
		for i := range candidate.Types {
			if candidate.Types[i].Key == "example.test/consumer.Opaque" {
				declared = &candidate.Types[i]
			}
		}
		if declared == nil || declared.Kind != "declared" || len(declared.Fields) != 0 || declared.Schemas == nil || !bytes.Equal(declared.Schemas.Input, schemas.Input) || !bytes.Equal(declared.Schemas.Output, schemas.Output) {
			t.Fatal("codec became inferred structure or lost directional assertion", declared)
		}
		var wire *WireType
		for _, command := range candidate.Commands {
			if command.Declaration.Type.Name == "ZSave" {
				field := fieldsByName(command.Fields)["payload"]
				wire = &field.Type
			}
		}
		if wire == nil || wire.Kind != "declared" || wire.Target != declared.Key || wire.Contract == nil || wire.Contract.Declared != "*example.test/consumer.Opaque" || wire.Contract.PointerDepth != 1 || wire.Contract.Schemas == nil {
			t.Fatal("codec declared identity or schemas lost", wire)
		}
		assertOpenAPICodecRefusalUnchanged(t, candidate)
		// Operation/type order must never turn refusal into partial publication.
		reordered := openAPICloneGraph(t, candidate)
		slices.Reverse(reordered.Commands)
		slices.Reverse(reordered.Types)
		slices.Reverse(reordered.Endpoints)
		slices.Reverse(reordered.Framework)
		assertOpenAPICodecRefusalUnchanged(t, reordered)
	}
}

func assertOpenAPICodecRefusalUnchanged(t *testing.T, graph *Graph) {
	t.Helper()
	before, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	document, err := renderOpenAPI(graph)
	if err == nil || len(document.bytes()) != 0 {
		t.Fatalf("opaque graph produced a whole/partial document: bytes=%s error=%v", document.bytes(), err)
	}
	after, err := json.Marshal(graph)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refusal mutated graph: %v", err)
	}
}
