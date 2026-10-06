// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"strings"
	"testing"
)

func TestModelBarrelCannotAlsoBeAnOutputDirectory(t *testing.T) {
	graph := modelGraph(modelNode("a", "Shop.Root", "A"), modelNode("b", "Shop.Child", "B"))
	graph.Profile.TypeScript.NamespaceRoots = []NamespaceRoot{{Namespace: "Shop.Root", Folder: ""}, {Namespace: "Shop.Child", Folder: "index.ts"}}
	outputs, err := renderTypeScriptModels(graph)
	if outputs != nil || err == nil || !strings.Contains(err.Error(), "file/directory collision") {
		t.Fatalf("output=%v error=%v", outputs, err)
	}
}

func TestModelRendererRejectsUnsupportedOutputModesWithoutAPlan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile TypeScriptProfile
	}{
		{"grouping", TypeScriptProfile{SourceGrouping: true}},
		{"interfaces", TypeScriptProfile{Interfaces: true}},
		{"library", TypeScriptProfile{Library: true}},
		{"excluded_reachable", TypeScriptProfile{ExcludeTypes: []string{"Shop.Model"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := modelGraph(modelNode("a", "Shop", "Model"))
			graph.Profile.TypeScript = tc.profile
			outputs, err := renderTypeScriptModels(graph)
			if err == nil || outputs != nil {
				t.Fatalf("output=%v error=%v", outputs, err)
			}
		})
	}
}

func TestModelRendererHandlesQuotedWireFieldsAndPrimitiveDictionaries(t *testing.T) {
	graph := modelGraph(modelNode("a", "Shop", "Model",
		scalarField("private", "string"), scalarField("line\n\"break", "boolean"),
		FieldDescriptor{Name: "counts", Type: WireType{Kind: "record", Element: &WireType{Kind: "number"}, Nullable: true}},
	))
	outputs, err := renderTypeScriptModels(graph)
	if err != nil {
		t.Fatal(err)
	}
	content := string(outputs[0].content)
	for _, expected := range []string{"\"private\"!: string;", "\"line\\n\\\"break\"!: boolean;", "@field(Object)", "counts?: Record<string, number>;"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
}
