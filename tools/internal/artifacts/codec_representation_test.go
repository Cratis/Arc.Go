// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

type traversalCodecWitness struct{ Exported string }

func (traversalCodecWitness) MarshalJSONWith(encode func(any) ([]byte, error)) ([]byte, error) {
	return encode("traversal string")
}

func (traversalCodecWitness) MarshalJSON() ([]byte, error) {
	return []byte(`"ordinary codec"`), nil
}

func TestTraversalCodecRuntimeOverridesStructAndOrdinaryCodec(t *testing.T) {
	value := traversalCodecWitness{Exported: "not an object"}
	for _, input := range []any{value, &value} {
		data, err := serialization.Marshal(input)
		if err != nil || string(data) != `"traversal string"` {
			t.Fatalf("runtime traversal output=%s error=%v", data, err)
		}
	}
}

func codecCompilerPackage(t *testing.T) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "codecs.go", `package fixture
 type Value struct { Exported string }
 func (Value) MarshalJSONWith(func(any) ([]byte, error)) ([]byte, error) { panic("must not execute") }
 type Pointer struct { Exported string }
 func (*Pointer) MarshalJSONWith(func(any) ([]byte, error)) ([]byte, error) { panic("must not execute") }
 type Alias = Value
 type PointerAlias = *Pointer
 type Promoted struct { Value; Exported string }
 type PromotedPointer struct { *Pointer; Exported string }
 type NewValue Value
 type Ordinary struct { Exported string }
 type Conventional int32
 func (Conventional) MarshalJSON() ([]byte, error) { panic("must not execute") }
 func (*Conventional) UnmarshalJSON([]byte) error { panic("must not execute") }
 func (Conventional) MarshalText() ([]byte, error) { panic("must not execute") }
 func (*Conventional) UnmarshalText([]byte) error { panic("must not execute") }
 type All struct { Value; Conventional }
 type Concept struct { Value string }
 func (Concept) ConceptValue() string { panic("must not execute") }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := types.Config{}
	pkg, err := config.Check("example.test/fixture", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestCodecMethodNamesInventoriesAliasesPointersAndPromotion(t *testing.T) {
	pkg := codecCompilerPackage(t)
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"Value", []string{"MarshalJSONWith"}},
		{"Pointer", []string{"MarshalJSONWith"}},
		{"Alias", []string{"MarshalJSONWith"}},
		{"PointerAlias", []string{"MarshalJSONWith"}},
		{"Promoted", []string{"MarshalJSONWith"}},
		{"PromotedPointer", []string{"MarshalJSONWith"}},
		{"Conventional", []string{"MarshalJSON", "MarshalText", "UnmarshalJSON", "UnmarshalText"}},
		{"All", []string{"MarshalJSON", "MarshalJSONWith", "MarshalText", "UnmarshalJSON", "UnmarshalText"}},
		{"NewValue", nil},
		{"Ordinary", nil},
		{"Concept", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := pkg.Scope().Lookup(tc.name).Type()
			for _, typ := range []types.Type{value, types.NewPointer(value), types.NewPointer(types.NewPointer(value))} {
				if got := codecMethodNames(typ); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("%s inventory=%v want=%v", typ, got, tc.want)
				}
			}
		})
	}
}

func TestCodecQueryRepresentationRetainsUnderlyingAndPointerFacts(t *testing.T) {
	pkg := codecCompilerPackage(t)
	for _, tc := range []struct {
		name string
		want QueryRuleRepresentation
	}{
		{"Alias", QueryRuleRepresentation{GoKind: "struct", PointerDepth: 2, CustomCodec: true}},
		{"PromotedPointer", QueryRuleRepresentation{GoKind: "struct", PointerDepth: 2, CustomCodec: true}},
		{"Conventional", QueryRuleRepresentation{GoKind: "int32", PointerDepth: 2, CustomCodec: true}},
		{"Concept", QueryRuleRepresentation{GoKind: "struct", PointerDepth: 2, CustomCodec: true}},
		{"Ordinary", QueryRuleRepresentation{GoKind: "struct", PointerDepth: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ := types.NewPointer(types.NewPointer(pkg.Scope().Lookup(tc.name).Type()))
			got := queryRuleRepresentation(typ)
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var restored QueryRuleRepresentation
			if err := json.Unmarshal(encoded, &restored); err != nil || restored != tc.want {
				t.Fatalf("representation=%+v want=%+v error=%v", restored, tc.want, err)
			}
		})
	}
}

const traversalCodecPanicMethod = `
func (Opaque) MarshalJSONWith(func(any) ([]byte, error)) ([]byte, error) { panic("analysis executed traversal hook") }
`

func codecCommandSource(declaration, fieldType string) string {
	return "//arc:namespace Shop\npackage consumer\n" + declaration + `
//arc:command
type Save struct { Payload ` + fieldType + ` }
func (Save) Handle() error { return nil }
`
}

func TestTraversalCodecBothGraphsRefuseInferenceWithoutExecutingBodies(t *testing.T) {
	for _, tc := range []struct{ name, declaration, fieldType string }{
		{"struct", "type Opaque struct { Exported string }" + traversalCodecPanicMethod, "Opaque"},
		{"pointer receiver", `type Opaque struct { Exported string }
func (*Opaque) MarshalJSONWith(func(any) ([]byte, error)) ([]byte, error) { panic("analysis executed traversal hook") }`, "*Opaque"},
		{"alias", "type Opaque struct { Exported string }" + traversalCodecPanicMethod + "type Alias = Opaque", "Alias"},
		{"promotion", "type Opaque struct { Exported string }" + traversalCodecPanicMethod + "type Outer struct { Opaque; Exported string }", "Outer"},
		{"concept", "type Opaque struct { Exported string }" + traversalCodecPanicMethod + `func (Opaque) ConceptValue() string { panic("analysis executed concept") }`, "**Opaque"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "input.go"), codecCommandSource(tc.declaration, tc.fieldType))
			analyses := graphPackages(t, dir, ".")
			for _, version := range []int{GraphVersion, ContractGraphVersion} {
				t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
					graph, err := buildGraph(analyses, ApplicationProfile{FormatVersion: version, Name: "codec-refusal"}, true)
					if graph != nil || err == nil || !strings.Contains(err.Error(), "opaque custom codec requires an explicit wire import mapping") || !strings.Contains(err.Error(), "MarshalJSONWith") || !strings.Contains(err.Error(), "example.test/consumer.") {
						t.Fatalf("opaque hook admitted: graph=%v error=%v", graph, err)
					}
				})
			}
		})
	}
}

func TestTraversalCodecOrdinaryStructAndEnumRemainAdmitted(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), codecCommandSource(`type Ordinary struct { Exported string }
//arc:enum
type State int32
const (Ready State = 1; Done State = 2)
`, "Ordinary; State State"))
	analyses := graphPackages(t, dir, ".")
	for _, version := range []int{GraphVersion, ContractGraphVersion} {
		graph, err := buildGraph(analyses, ApplicationProfile{FormatVersion: version, Name: "codec-controls"}, true)
		if err != nil {
			t.Fatal(err)
		}
		fields := fieldsByName(graph.Commands[0].Fields)
		if fields["payload"].Type.Kind != "model" || fields["state"].Type.Kind != "enum" {
			t.Fatal(fields)
		}
	}
}

func TestCodecEnumUnmarshalJSONRemainsOpaque(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), codecCommandSource(`//arc:enum
type State int32
const Ready State = 1
func (*State) UnmarshalJSON([]byte) error { panic("analysis executed enum parser") }
`, "State"))
	analyses := graphPackages(t, dir, ".")
	for _, version := range []int{GraphVersion, ContractGraphVersion} {
		graph, err := buildGraph(analyses, ApplicationProfile{FormatVersion: version, Name: "enum-opaque"}, true)
		if graph != nil || err == nil || !strings.Contains(err.Error(), "opaque custom codec requires an explicit wire import mapping") {
			t.Fatalf("handwritten enum codec admitted: graph=%v error=%v", graph, err)
		}
	}
}

func TestTraversalCodecImportMappingRemainsExternal(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), codecCommandSource("type Opaque struct { Exported string }"+traversalCodecPanicMethod, "Opaque"))
	analyses := graphPackages(t, dir, ".")
	for _, version := range []int{GraphVersion, ContractGraphVersion} {
		profile := ApplicationProfile{FormatVersion: version, Name: "mapped-codec", Imports: map[string]ImportMapping{"example.test/consumer.Opaque": {Module: "@application/codec", Type: "Opaque", Constructor: "String"}}}
		graph, err := buildGraph(analyses, profile, true)
		if err != nil {
			t.Fatal(err)
		}
		wire := graph.Commands[0].Fields[0].Type
		if wire.Kind != "model" || wire.Target != "example.test/consumer.Opaque" {
			t.Fatal(wire)
		}
		for _, node := range graph.Types {
			if node.Key == wire.Target && (node.Kind != "external" || node.Import == nil || node.Schemas != nil || len(node.Fields) != 0) {
				t.Fatal("import mapping reinterpreted as schema evidence", node)
			}
		}
	}
}
