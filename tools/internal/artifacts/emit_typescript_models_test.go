// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
)

func TestTypeScriptModelFixture(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "Models")
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), string(get(t, filepath.Join(fixture, "input.go.txt"))))
	graph, err := buildGraph(graphPackages(t, dir, "."), ApplicationProfile{FormatVersion: GraphVersion, Name: "wire-model-fixture"}, true)
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := renderTypeScriptModels(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 7 {
		t.Fatalf("incomplete model inventory: %d", len(outputs))
	}
	// Explicit fixture-only update, never a production publisher. Ordinary tests
	// compare every byte and the exact inventory and do not require Node.
	snapshot := filepath.Join(fixture, "Generated")
	update := os.Getenv("ARC_UPDATE_MODEL_FIXTURE") == "1"
	var expected []string
	for _, output := range outputs {
		expected = append(expected, filepath.FromSlash(output.path))
		file := filepath.Join(snapshot, filepath.FromSlash(output.path))
		if update {
			put(t, file, string(output.content))
		}
		if !bytes.Equal(get(t, file), output.content) {
			t.Fatalf("stale model fixture %s", file)
		}
	}
	var actual []string
	if err := filepath.WalkDir(snapshot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			rel, err := filepath.Rel(snapshot, path)
			if err != nil {
				return err
			}
			actual = append(actual, rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("inventory: got %v want %v", actual, expected)
	}
	reversed := *graph
	reversed.Types = append([]TypeDescriptor(nil), graph.Types...)
	for left, right := 0, len(reversed.Types)-1; left < right; left, right = left+1, right-1 {
		reversed.Types[left], reversed.Types[right] = reversed.Types[right], reversed.Types[left]
	}
	again, err := renderTypeScriptModels(&reversed)
	if err != nil || !reflect.DeepEqual(outputs, again) {
		t.Fatalf("package/type order changed output: %v", err)
	}
}

func modelGraph(nodes ...TypeDescriptor) *Graph {
	return &Graph{FormatVersion: GraphVersion, Profile: ApplicationProfile{FormatVersion: GraphVersion, Name: "test"}, Types: nodes}
}
func modelNode(key, namespace, name string, fields ...FieldDescriptor) TypeDescriptor {
	return TypeDescriptor{Key: key, Name: metadata.TypeName{Namespace: namespace, Name: name}, Kind: "model", Fields: fields}
}
func scalarField(name, kind string) FieldDescriptor {
	return FieldDescriptor{Name: name, Type: WireType{Kind: kind}}
}

func TestModelRendererRejectsCompleteUnsupportedPlans(t *testing.T) {
	base := modelNode("base", "Shop", "Notice", scalarField("title", "string"))
	base.Interface = "INotice"
	derivative := modelNode("derived", "Shop", "UrgentNotice", scalarField("title", "string"))
	derivative.Interface = "INotice"
	derivative.Base = "base"
	derivative.DerivedID = "1578f20a-cd63-456f-98aa-c97daf05d0fa"
	tests := []struct {
		name, message string
		nodes         []TypeDescriptor
	}{
		{"external", "unsupported model kind", []TypeDescriptor{{Key: "opaque", Name: metadata.TypeName{Name: "Opaque"}, Kind: "external"}}},
		{"unknown", "unsupported wire kind", []TypeDescriptor{modelNode("a", "Shop", "Model", scalarField("x", "any"))}},
		{"missing", "missing or mismatched", []TypeDescriptor{modelNode("a", "Shop", "Model", FieldDescriptor{Name: "x", Type: WireType{Kind: "model", Target: "absent"}})}},
		{"nested_array", "nested/nullable collection", []TypeDescriptor{modelNode("a", "Shop", "Model", FieldDescriptor{Name: "x", Type: WireType{Kind: "array", Element: &WireType{Kind: "array", Element: &WireType{Kind: "string"}}}})}},
		{"nullable_element", "nested/nullable collection", []TypeDescriptor{modelNode("a", "Shop", "Model", FieldDescriptor{Name: "x", Type: WireType{Kind: "array", Element: &WireType{Kind: "Guid", Nullable: true}}})}},
		{"case_collision", "output collision", []TypeDescriptor{modelNode("a", "Shop", "Model"), modelNode("b", "Shop", "model")}},
		{"directory_case_collision", "directory case collision", []TypeDescriptor{modelNode("a", "Shop.Models", "A"), modelNode("b", "Shop.models", "B")}},
		{"builtin_shadow", "invalid TypeScript export", []TypeDescriptor{modelNode("a", "Shop", "Date", scalarField("created", "Date"))}},
		{"wide_enum", "safe number/bitwise limits", []TypeDescriptor{{Key: "enum", Name: metadata.TypeName{Namespace: "Shop", Name: "State"}, Kind: "enum", Members: []EnumMember{{Name: "wide", Value: "9007199254740992"}}}}},
		{"wide_flags", "safe number/bitwise limits", []TypeDescriptor{{Key: "enum", Name: metadata.TypeName{Namespace: "Shop", Name: "Access"}, Kind: "enum", Flags: true, Members: []EnumMember{{Name: "wide", Value: "2147483648"}}}}},
		{"enum_member", "invalid or duplicate enum member", []TypeDescriptor{{Key: "enum", Name: metadata.TypeName{Namespace: "Shop", Name: "State"}, Kind: "enum", Members: []EnumMember{{Name: "__proto__", Value: "1"}}}}},
		{"flags_export", "barrel export collision", []TypeDescriptor{modelNode("a", "Shop", "allAccess"), {Key: "enum", Name: metadata.TypeName{Namespace: "Shop", Name: "Access"}, Kind: "enum", Flags: true, Members: []EnumMember{{Name: "read", Value: "1"}}}}},
		{"cycle", "constructor reference cycle", []TypeDescriptor{modelNode("a", "Shop", "Model", FieldDescriptor{Name: "x", Type: WireType{Kind: "model", Target: "a"}})}},
		{"reserved", "invalid or duplicate wire field", []TypeDescriptor{modelNode("a", "Shop", "Model", scalarField("__proto__", "string"))}},
		{"duplicate_key", "duplicate model key", []TypeDescriptor{modelNode("a", "Shop", "A"), modelNode("a", "Shop", "B")}},
		{"invalid_identifier", "invalid TypeScript export", []TypeDescriptor{modelNode("a", "Shop", "class")}},
		{"barrel_case_collision", "output collision", []TypeDescriptor{modelNode("a", "Shop", "Index")}},
		{"invalid_namespace", "unsafe TypeScript namespace", []TypeDescriptor{modelNode("a", "Shop../escape", "A")}},
	}
	badID := derivative
	badID.DerivedID = "not-a-uuid"
	tests = append(tests, struct {
		name, message string
		nodes         []TypeDescriptor
	}{"derived_uuid", "invalid derived UUID", []TypeDescriptor{base, badID}})
	altered := derivative
	altered.Fields = []FieldDescriptor{scalarField("title", "number")}
	tests = append(tests, struct {
		name, message string
		nodes         []TypeDescriptor
	}{"derived_field", "changes base field", []TypeDescriptor{base, altered}})
	polymorphic := derivative
	polymorphic.Base = ""
	tests = append(tests, struct {
		name, message string
		nodes         []TypeDescriptor
	}{"polymorphic", "explicit concrete base", []TypeDescriptor{polymorphic}})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			graph := modelGraph(append([]TypeDescriptor{modelNode("valid", "Shop", "Valid")}, tc.nodes...)...)
			output, err := renderTypeScriptModels(graph)
			if output != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("output %v, error %v; want %q and no output", output, err, tc.message)
			}
		})
	}
}

func TestModelLayoutAndAliasedImports(t *testing.T) {
	graph := modelGraph(modelNode("a", "Shop.Root", "Guid", scalarField("ID", "Guid"), FieldDescriptor{Name: "other", Type: WireType{Kind: "model", Target: "b"}}), modelNode("b", "Shop.Other", "Guid"))
	skip := 1
	graph.Profile.TypeScript = TypeScriptProfile{SegmentsToSkip: &skip, ProxyFileSuffix: true, NamespaceRoots: []NamespaceRoot{{Namespace: "Shop.Root", Folder: "ui"}}}
	outputs, err := renderTypeScriptModels(graph)
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for _, output := range outputs {
		if output.path == "ui/Guid.proxy.ts" {
			content = string(output.content)
		}
	}
	for _, expected := range []string{"import { Guid as Guid_2 } from \"../Other/Guid.proxy\";", "import { Guid as Guid_3 } from \"@cratis/fundamentals\";", "@field(Guid_3)", "other!: Guid_2"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
}
