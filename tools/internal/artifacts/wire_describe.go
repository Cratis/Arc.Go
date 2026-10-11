// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
	"slices"

	"github.com/cratis/arc.go/metadata"
)

// namedDescriber describes one kind of named type. It reports handled=false
// when the named type is not of its kind so the next describer is tried.
type namedDescriber func(t types.Type, named *types.Named, key string) (WireType, bool, error)

func (w *wireAnalyzer) describeValue(t types.Type) (WireType, error) {
	// The traversal hook owns runtime output before scalar/struct inference,
	// including application concepts. Keep declared schemas and import mappings
	// on the existing named-type path instead of treating a marker as evidence.
	if wire, handled, err := w.describeConcept(t); handled {
		return wire, err
	}
	t = types.Unalias(t)
	if pointer, ok := t.Underlying().(*types.Pointer); ok {
		wire, err := w.describe(pointer.Elem())
		wire.Nullable = true
		return wire, err
	}
	if namedType(t, "time", "Time") {
		return WireType{Kind: "Date"}, nil
	}
	if named, ok := t.(*types.Named); ok {
		if wire, handled, err := w.describeNamed(t, named); handled {
			return wire, err
		}
		t = named.Underlying()
	}
	return w.describeUnderlying(t)
}

func (w *wireAnalyzer) describeConcept(t types.Type) (WireType, bool, error) {
	if slices.Contains(codecMethodNames(t), "MarshalJSONWith") {
		return WireType{}, false, nil
	}
	wire, recognized, err := conceptWire(t)
	if err != nil {
		return WireType{}, true, fmt.Errorf("wire type %s: %w", typeKey(t), err)
	}
	if !recognized {
		return WireType{}, false, nil
	}
	if w.typescript && wire.Kind == "number" {
		w.graph.Diagnostics = append(w.graph.Diagnostics, "number precision: concept "+typeKey(t)+" uses JavaScript number; enforce safe range in domain validation")
	}
	return wire, true, nil
}

func (w *wireAnalyzer) describeNamed(t types.Type, named *types.Named) (WireType, bool, error) {
	key := typeKey(t)
	describers := []namedDescriber{
		w.describeOptional,
		w.describeDeclaredSchema,
		w.describeImport,
		w.describeInterfaceModel,
		w.describeCodecType,
		w.describeEnumType,
		w.describeStructType,
	}
	for _, describe := range describers {
		if wire, handled, err := describe(t, named, key); handled {
			return wire, true, err
		}
	}
	return WireType{}, false, nil
}

func (w *wireAnalyzer) describeOptional(t types.Type, named *types.Named, _ string) (WireType, bool, error) {
	if !namedType(t, runtimePath+"/serialization", "Optional") {
		return WireType{}, false, nil
	}
	if w.typescript {
		return WireType{}, true, w.fail(t, "serialization.Optional requires a presence-compatible surface; strict C# proxy mode does not erase explicit null")
	}
	if named.TypeArgs().Len() != 1 {
		return WireType{}, true, w.fail(t, "invalid Optional type arguments")
	}
	element, err := w.describe(named.TypeArgs().At(0))
	return WireType{Kind: "optional", Element: &element, Nullable: true}, true, err
}

func (w *wireAnalyzer) describeDeclaredSchema(t types.Type, named *types.Named, key string) (WireType, bool, error) {
	schemas, exists := w.profile.WireSchemas[key]
	if !exists || w.typescript {
		return WireType{}, false, nil
	}
	w.nodes[key] = &TypeDescriptor{Key: key, Name: metadata.TypeName{Name: named.Obj().Name()}, Kind: "declared", Schemas: &schemas}
	w.compilerTypes[key] = t
	return WireType{Kind: "declared", Target: key}, true, nil
}

func (w *wireAnalyzer) describeImport(t types.Type, _ *types.Named, key string) (WireType, bool, error) {
	mapped := w.profile.Imports[key]
	if mapped.Module == "" {
		return WireType{}, false, nil
	}
	if _, exists := w.profile.WireSchemas[key]; w.profile.OpenAPI != nil && !exists {
		return WireType{}, true, w.fail(t, "opaque import requires explicit input/output wireSchemas; TS imports are not schema evidence")
	}
	if w.nodes[key] == nil {
		w.nodes[key] = &TypeDescriptor{Key: key, Name: metadata.TypeName{Name: mapped.Type}, Kind: "external", Import: &mapped}
	}
	w.compilerTypes[key] = t
	return WireType{Kind: "model", Target: key}, true, nil
}

func (w *wireAnalyzer) describeInterfaceModel(_ types.Type, _ *types.Named, key string) (WireType, bool, error) {
	model := w.interfaceModels[key]
	if model == nil {
		return WireType{}, false, nil
	}
	wire, err := w.describe(model.typ)
	return wire, true, err
}

func isOwnedInt32EnumParser(methods []string, declaration *model) bool {
	return len(methods) == 1 && methods[0] == "UnmarshalJSON" && declaration != nil && declaration.d.kind == "enum" && declaration.d.parse == "int32"
}

func (w *wireAnalyzer) describeCodecType(t types.Type, named *types.Named, key string) (WireType, bool, error) {
	methods := codecMethodNames(named)
	if len(methods) == 0 {
		return WireType{}, false, nil
	}
	declaration := w.declarations[key]
	if isOwnedInt32EnumParser(methods, declaration) {
		wire, owned, err := w.describeParsedEnum(t, named, declaration)
		if err != nil || owned {
			return wire, true, err
		}
	}
	if slices.Contains(methods, "MarshalJSONWith") {
		return WireType{}, true, w.fail(t, "opaque custom codec requires an explicit wire import mapping: %s implements MarshalJSONWith (or declare explicit input/output schemas with contract-v2 wireSchemas)", key)
	}
	return WireType{}, true, w.fail(t, "opaque custom codec requires an explicit wire import mapping")
}

func (w *wireAnalyzer) describeParsedEnum(t types.Type, named *types.Named, declaration *model) (WireType, bool, error) {
	members, err := int32EnumDeclarationMembers(declaration)
	if err != nil {
		return WireType{}, false, w.fail(t, "%v", err)
	}
	owned, err := w.ownsEnumParser(named, members)
	if err != nil {
		return WireType{}, false, w.fail(t, "%v", err)
	}
	if !owned {
		return WireType{}, false, nil
	}
	wire, err := w.enum(declaration)
	return wire, true, err
}

func (w *wireAnalyzer) describeEnumType(_ types.Type, _ *types.Named, key string) (WireType, bool, error) {
	declaration := w.declarations[key]
	if declaration == nil || declaration.d.kind != "enum" {
		return WireType{}, false, nil
	}
	wire, err := w.enum(declaration)
	return wire, true, err
}

func isByteElement(t types.Type) bool {
	basic, ok := types.Unalias(t).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Uint8
}

func isScalarWireKind(kind string) bool {
	return kind == "string" || kind == "number" || kind == "boolean" || kind == "enum"
}

func isPrecisionRiskKind(kind types.BasicKind) bool {
	return kind == types.Int64 || kind == types.Uint64 || kind == types.Int || kind == types.Uint
}

func (w *wireAnalyzer) describeUnderlying(t types.Type) (WireType, error) {
	switch t := t.(type) {
	case *types.Basic:
		if wire, ok := w.describeBasic(t); ok {
			return wire, nil
		}
	case *types.Slice:
		return w.describeSlice(t)
	case *types.Array:
		return w.describeArray(t)
	case *types.Map:
		return w.describeMap(t)
	}
	return WireType{}, w.fail(t, "unsupported frontend wire type (no implicit any)")
}

func (w *wireAnalyzer) describeBasic(t *types.Basic) (WireType, bool) {
	switch {
	case t.Info()&types.IsString != 0:
		return WireType{Kind: "string"}, true
	case t.Info()&types.IsBoolean != 0:
		return WireType{Kind: "boolean"}, true
	case t.Info()&(types.IsInteger|types.IsFloat) != 0:
		if w.typescript && isPrecisionRiskKind(t.Kind()) {
			w.graph.Diagnostics = append(w.graph.Diagnostics, "number precision: "+t.Name()+" values must remain within JavaScript's safe integer range")
		}
		return WireType{Kind: "number"}, true
	}
	return WireType{}, false
}

func (w *wireAnalyzer) describeSlice(t *types.Slice) (WireType, error) {
	if isByteElement(t.Elem()) {
		return WireType{}, w.fail(t, "binary values require an explicit wire contract")
	}
	element, err := w.describe(t.Elem())
	return WireType{Kind: "array", Element: &element, Nullable: true}, err
}

func (w *wireAnalyzer) describeArray(t *types.Array) (WireType, error) {
	if isByteElement(t.Elem()) && !w.contract {
		return WireType{}, w.fail(t, "binary arrays require an explicit wire contract (not inferred UUID)")
	}
	element, err := w.describe(t.Elem())
	return WireType{Kind: "array", Element: &element}, err
}

func (w *wireAnalyzer) describeMap(t *types.Map) (WireType, error) {
	if basic, ok := types.Unalias(t.Key()).Underlying().(*types.Basic); !ok || basic.Kind() != types.String {
		return WireType{}, w.fail(t, "non-string dictionary keys require an explicit backend codec and wire mapping")
	}
	element, err := w.describe(t.Elem())
	if err != nil {
		return WireType{}, err
	}
	if w.typescript && !isScalarWireKind(element.Kind) {
		return WireType{}, w.fail(t, "rich dictionary values cannot hydrate through Object metadata; an explicit codec/mapping is required")
	}
	return WireType{Kind: "record", Element: &element, Nullable: true}, nil
}
