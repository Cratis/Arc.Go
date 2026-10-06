// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"errors"
	"go/ast"
	"go/constant"
	"go/types"
	"testing"

	"github.com/cratis/fundamentals.go/concepts"
	"github.com/cratis/fundamentals.go/concepts/conceptstypes"
	"golang.org/x/tools/go/packages"
)

// Load the actual pinned dependency corpus; do not vendor an independently
// evolving recognizer or execute its panic-bearing application declarations.
func TestConceptProjectionUsesTheSharedDeclarationCorpus(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{Context: t.Context(), Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps}, "github.com/cratis/fundamentals.go/concepts/internal/corpus")
	if err != nil || len(loaded) != 1 || len(loaded[0].Errors) != 0 {
		t.Fatal(loaded, err)
	}
	pkg := loaded[0]
	count := 0
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || len(literal.Elts) != 9 {
				return true
			}
			typ := pkg.TypesInfo.TypeOf(literal)
			if typ == nil {
				return true
			}
			named, ok := types.Unalias(typ).(*types.Named)
			if !ok || named.Obj().Name() != "Case" {
				return true
			}
			text := func(index int) string { return constant.StringVal(pkg.TypesInfo.Types[literal.Elts[index]].Value) }
			name, typeName, reason := text(0), text(1), text(6)
			kindValue, ok := constant.Uint64Val(pkg.TypesInfo.Types[literal.Elts[5]].Value)
			if !ok {
				t.Fatal("missing scalar kind", name)
			}
			var input types.Type
			if typeName != "" {
				object := pkg.Types.Scope().Lookup(typeName)
				if object == nil {
					t.Fatal("missing shared type", typeName)
				}
				input = object.Type()
			}
			count++
			t.Run(name, func(t *testing.T) {
				for depth := 0; depth < 3; depth++ {
					candidate := input
					if candidate == nil && depth != 0 {
						continue
					}
					for range depth {
						candidate = types.NewPointer(candidate)
					}
					wire, recognized, err := conceptWire(candidate)
					if reason != "" {
						var failure *conceptstypes.TypeError
						if !errors.Is(err, concepts.ErrInvalidConcept) || !errors.As(err, &failure) || string(failure.Reason) != reason || recognized {
							t.Fatalf("shared rejection: %v, %v, %v", wire, recognized, err)
						}
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					kind := concepts.ScalarKind(kindValue)
					if kind == concepts.KindInvalid {
						if recognized || wire != (WireType{}) {
							t.Fatal("ordinary declaration recognized", wire)
						}
						continue
					}
					expected := "number"
					switch kind {
					case concepts.KindString:
						expected = "string"
					case concepts.KindBool:
						expected = "boolean"
					case concepts.KindUUID:
						expected = "Guid"
					case concepts.KindDateOnly:
						expected = "DateOnly"
					case concepts.KindTimeOnly:
						expected = "TimeOnly"
					case concepts.KindTimeSpan:
						expected = "TimeSpan"
					}
					originalDepth, _ := constant.Int64Val(pkg.TypesInfo.Types[literal.Elts[8]].Value)
					if !recognized || wire.Kind != expected || wire.Nullable != (originalDepth+int64(depth) > 0) || wire.Target != "" {
						t.Fatal("wrong scalar projection", wire, kind)
					}
				}
			})
			return false
		})
	}
	if count < 110 {
		t.Fatalf("incomplete shared declaration corpus: %d", count)
	}
}
