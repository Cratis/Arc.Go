// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
	"math/big"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/fundamentals.go/concepts/conceptstypes"
)

// ScalarContract preserves Go representation, not JavaScript capability limits.
// Integer bounds are exact decimal text; they must never pass through float64.
type ScalarContract struct {
	GoKind         string   `json:"goKind"`
	Representation string   `json:"representation"`
	Bits           int      `json:"bits,omitempty"`
	Unsigned       bool     `json:"unsigned,omitempty"`
	Minimum        string   `json:"minimum,omitempty"`
	Maximum        string   `json:"maximum,omitempty"`
	SpecialValues  []string `json:"specialValues,omitempty"`
	Format         string   `json:"format,omitempty"`
}

// WireContract describes a value independently of its containing property.
// Nullable is the legacy TS hint; InputNull and OutputNull are codec facts.
type WireContract struct {
	Declared         string          `json:"declared"`
	Scalar           *ScalarContract `json:"scalar,omitempty"`
	Concept          string          `json:"concept,omitempty"`
	FixedLength      *int64          `json:"fixedLength,omitempty"`
	ByteArray        bool            `json:"byteArray,omitempty"`
	ArrayInputLength string          `json:"arrayInputLength,omitempty"`
	InputNull        bool            `json:"inputNull"`
	OutputNull       bool            `json:"outputNull"`
	Presence         string          `json:"presence"`
	PointerDepth     int             `json:"pointerDepth,omitempty"`
	Schemas          *WireSchemas    `json:"schemas,omitempty"`
}

// FieldPresence separates JSON binding and publication from client optionality.
// Missing inputs start with a fresh zero value. OutputRequired means the property
// is always present, not that its value is non-null or application-valid.
type FieldPresence struct {
	InputRequired          bool   `json:"inputRequired"`
	InputMissing           string `json:"inputMissing"`
	InputNull              bool   `json:"inputNull"`
	OutputRequired         bool   `json:"outputRequired"`
	OutputNull             bool   `json:"outputNull"`
	OmitNil                bool   `json:"omitNil"`
	OmitNull               bool   `json:"omitNull,omitempty"`
	OmitMissing            bool   `json:"omitMissing"`
	OmitEmpty              bool   `json:"omitEmpty"`
	OmitZero               bool   `json:"omitZero"`
	EmbeddedParentNullable bool   `json:"embeddedParentNullable"`
}

// QueryBinding records builtin reader semantics, separately from JSON fields.
// Requiredness/defaults remain on FieldDescriptor and are not schema validators.
type QueryBinding struct {
	Reader           string `json:"reader"`
	Encoding         string `json:"encoding"`
	CaseInsensitive  bool   `json:"caseInsensitive"`
	DuplicateNames   string `json:"duplicateNames"`
	RepeatedValues   string `json:"repeatedValues"`
	EmptyAsMissing   bool   `json:"emptyAsMissing"`
	NullAsMissing    bool   `json:"nullAsMissing"`
	PreservePresence bool   `json:"preservePresence"`
	Missing          string `json:"missing"`
	FixedLength      *int64 `json:"fixedLength,omitempty"`
}

// DerivedContract records concrete codec behavior. Default models reject a
// supplied discriminator; nondefault concrete models accept its absence on input.
type DerivedContract struct {
	Property       string `json:"property"`
	Default        bool   `json:"default"`
	ID             string `json:"id,omitempty"`
	InputRequired  bool   `json:"inputRequired"`
	OutputRequired bool   `json:"outputRequired"`
}

func validateContractDerivatives(nodes []TypeDescriptor) error {
	ids := map[string]string{}
	for _, node := range nodes {
		if node.Discriminator == nil {
			continue
		}
		for _, field := range node.Fields {
			if field.Name == "_derivedTypeId" {
				return fmt.Errorf("%s: derived discriminator is a reserved wire field", node.Key)
			}
		}
		if node.Discriminator.Default {
			continue
		}
		id, err := concepts.ParseUUID(node.DerivedID)
		if err != nil || id.String() != node.DerivedID {
			return fmt.Errorf("%s: derived ID requires a canonical UUID", node.Key)
		}
		if previous := ids[node.DerivedID]; previous != "" {
			return fmt.Errorf("duplicate derived ID for %s and %s", previous, node.Key)
		}
		ids[node.DerivedID] = node.Key
	}
	return nil
}

func scalarContract(basic *types.Basic, sizes types.Sizes) (*ScalarContract, error) {
	scalar := &ScalarContract{GoKind: basic.Name()}
	switch {
	case basic.Info()&types.IsString != 0:
		scalar.Representation = "string"
	case basic.Info()&types.IsBoolean != 0:
		scalar.Representation = "boolean"
	case basic.Info()&(types.IsInteger|types.IsFloat) != 0:
		if sizes == nil {
			return nil, fmt.Errorf("exact scalar widths require target compiler sizes")
		}
		scalar.Bits = int(sizes.Sizeof(basic) * 8)
		if basic.Info()&types.IsFloat != 0 {
			scalar.Representation = "float"
			scalar.SpecialValues = []string{"NaN", "Infinity", "-Infinity"}
			return scalar, nil
		}
		scalar.Representation = "integer"
		scalar.Unsigned = basic.Info()&types.IsUnsigned != 0
		bits := scalar.Bits
		if !scalar.Unsigned {
			bits--
		}
		upper := new(big.Int).Lsh(big.NewInt(1), uint(bits))
		if scalar.Unsigned {
			scalar.Minimum = "0"
		} else {
			scalar.Minimum = new(big.Int).Neg(new(big.Int).Set(upper)).String()
		}
		scalar.Maximum = upper.Sub(upper, big.NewInt(1)).String()
	default:
		return nil, fmt.Errorf("unsupported scalar %s", basic)
	}
	return scalar, nil
}

func embeddedNullable(t types.Type, index []int) bool {
	for _, i := range index[:len(index)-1] {
		st := types.Unalias(t).Underlying().(*types.Struct)
		t = types.Unalias(st.Field(i).Type())
		if _, ok := t.(*types.Pointer); ok {
			return true
		}
	}
	return false
}

func hasMethod(t types.Type, name string) bool {
	return types.NewMethodSet(t).Lookup(nil, name) != nil || types.NewMethodSet(types.NewPointer(t)).Lookup(nil, name) != nil
}

func (w *wireAnalyzer) describe(t types.Type) (WireType, error) {
	w.seenTypes[typeKey(t)] = true
	if w.contract {
		w.compilerTypes[typeKey(t)] = t
	}
	wire, err := w.describeValue(t)
	if err != nil || !w.contract {
		return wire, err
	}
	contract := &WireContract{Declared: typeKey(t), InputNull: wire.Nullable || nilableGo(t), OutputNull: wire.Nullable || nilableGo(t), Presence: "value"}
	if wire.Contract != nil {
		*contract = *wire.Contract
		contract.Declared = typeKey(t)
		contract.InputNull = wire.Nullable || nilableGo(t) || contract.InputNull
		contract.OutputNull = wire.Nullable || nilableGo(t) || contract.OutputNull
	}
	wire.Contract = contract
	base := types.Unalias(t)
	contract.PointerDepth = 0
	for {
		pointer, ok := base.(*types.Pointer)
		if !ok {
			break
		}
		contract.PointerDepth++
		base = types.Unalias(pointer.Elem())
	}
	w.seenTypes[typeKey(base)] = true
	if schemas, exists := w.profile.WireSchemas[typeKey(base)]; exists {
		copy := schemas
		contract.Schemas = &copy
		contract.InputNull = schemaAcceptsNull(schemas.Input) || wire.Nullable
		contract.OutputNull = schemaAcceptsNull(schemas.Output) || wire.Nullable
	}
	representation, recognized, err := conceptstypes.Underlying(t)
	if err != nil {
		return WireType{}, err
	}
	if recognized {
		contract.Concept = typeKey(representation.Declared)
		if hasMethod(representation.Declared, "ConceptValue") && contract.Schemas == nil {
			return WireType{}, w.fail(t, "concept codec requires explicit input/output wireSchemas; scalar identity alone does not prove codec acceptance")
		}
		// Known scalar codecs take precedence over primitive backing types:
		// TimeSpan is int64-backed, but its wire representation is a string.
		format := map[string]string{"Guid": "uuid", "DateOnly": "date", "TimeOnly": "local-time", "TimeSpan": "dotnet-time-span"}[wire.Kind]
		if format != "" {
			contract.Scalar = &ScalarContract{GoKind: typeKey(representation.Type), Representation: "string", Format: format}
		} else if basic, ok := representation.Type.Underlying().(*types.Basic); ok {
			contract.Scalar, err = scalarContract(basic, w.sizes)
		}
		return wire, err
	}
	if wire.Kind == "optional" {
		contract.Presence = "missing-null-value"
		contract.InputNull = true
		contract.OutputNull = true
		return wire, nil
	}
	if namedType(base, "time", "Time") {
		contract.InputNull = true
		contract.Scalar = &ScalarContract{GoKind: "time.Time", Representation: "string", Format: "date-time"}
		return wire, nil
	}
	if basic, ok := base.Underlying().(*types.Basic); ok {
		contract.Scalar, err = scalarContract(basic, w.sizes)
	}
	if array, ok := base.Underlying().(*types.Array); ok {
		length := array.Len()
		contract.FixedLength = &length
		contract.ArrayInputLength = "exact"
		if basic, ok := types.Unalias(array.Elem()).Underlying().(*types.Basic); ok && basic.Kind() == types.Uint8 {
			contract.ByteArray = true
			// Arc delegates byte arrays to encoding/json: input zero-fills or
			// truncates, while output still has exactly the declared length.
			contract.ArrayInputLength = "zero-fill-or-truncate"
		}
	}
	return wire, err
}

func emptyOmission(t types.Type) bool {
	switch t := types.Unalias(t).Underlying().(type) {
	case *types.Struct:
		return false
	case *types.Array:
		return t.Len() == 0
	default:
		return true
	}
}

func nilableGo(t types.Type) bool {
	switch types.Unalias(t).Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Interface:
		return true
	}
	return false
}

func unwrapOptional(t types.Type) types.Type {
	t = types.Unalias(t)
	if pointer, ok := t.(*types.Pointer); ok {
		return types.NewPointer(unwrapOptional(pointer.Elem()))
	}
	if named, ok := t.(*types.Named); ok && namedType(named, runtimePath+"/serialization", "Optional") && named.TypeArgs().Len() == 1 {
		return unwrapOptional(named.TypeArgs().At(0))
	}
	return t
}

func validateBuiltinQueryType(t types.Type, collection bool) error {
	t = unwrapOptional(t)
	if pointer, ok := t.(*types.Pointer); ok {
		return validateBuiltinQueryType(pointer.Elem(), collection)
	}
	if _, recognized, err := conceptstypes.Underlying(t); err != nil {
		return err
	} else if recognized {
		return nil
	}
	if hasMethod(t, "UnmarshalText") {
		return nil
	}
	switch t := t.Underlying().(type) {
	case *types.Basic:
		if t.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0 && t.Kind() != types.Uintptr {
			return nil
		}
	case *types.Slice:
		if !collection {
			return validateBuiltinQueryType(t.Elem(), true)
		}
	case *types.Array:
		if !collection {
			return validateBuiltinQueryType(t.Elem(), true)
		}
	}
	return fmt.Errorf("unsupported builtin query binding type %s; an explicit binder contract is required", t)
}

func (w *wireAnalyzer) declaredResponse(field ResponseField) (WireType, error) {
	if w.typescript {
		return WireType{}, fmt.Errorf("responseFields schema assertions do not establish TypeScript hydration capability; use a proven typed response")
	}
	if field.Type != "" {
		t, err := w.reference(nil, field.Type)
		if err != nil {
			return WireType{}, err
		}
		return w.describe(t)
	}
	schemas := &WireSchemas{Input: field.Schema, Output: field.Schema}
	return WireType{Kind: "declared", Contract: &WireContract{Presence: "value", Schemas: schemas, InputNull: schemaAcceptsNull(field.Schema), OutputNull: schemaAcceptsNull(field.Schema)}}, nil
}
