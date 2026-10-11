// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

// reservedQueryParameters are lower-cased names owned by the query pipeline.
var reservedQueryParameters = []string{"page", "pagesize", "sortby", "sortdirection", "waitforfirstresult", "waitforfirstresulttimeout"}

func (w *wireAnalyzer) fields(t types.Type, arguments bool) ([]FieldDescriptor, error) {
	members, err := compilerFields(t)
	if err != nil {
		return nil, w.fail(t, "%v", err)
	}
	var fields []FieldDescriptor
	seenArgs := map[string]bool{}
	for _, member := range members {
		field, err := w.describeField(t, member, arguments, seenArgs)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// wireMember is one compiler-selected struct member.
type wireMember = modelshape.WireField[types.Type]

// fieldStep refines a field descriptor from one aspect of its member.
type fieldStep func(t types.Type, member wireMember, wire WireType, field *FieldDescriptor) error

func (w *wireAnalyzer) describeField(t types.Type, member wireMember, arguments bool, seenArgs map[string]bool) (FieldDescriptor, error) {
	wire, err := w.describe(member.Type)
	if err != nil {
		return FieldDescriptor{}, fmt.Errorf("wire field %s.%s: %w", typeKey(t), member.Name, err)
	}
	field := FieldDescriptor{Name: member.Name, Type: wire, Optional: wire.Nullable || member.OmitEmpty || member.OmitZero, OmitEmpty: member.OmitEmpty, OmitZero: member.OmitZero}
	steps := []fieldStep{w.applyPresence, w.rejectReservedField, w.applySortable, applyIdentity, w.applyRules}
	for _, step := range steps {
		if err := step(t, member, wire, &field); err != nil {
			return FieldDescriptor{}, err
		}
	}
	if !arguments {
		return field, nil
	}
	if err := w.applyQueryArgument(t, member, &field, seenArgs); err != nil {
		return FieldDescriptor{}, err
	}
	return field, nil
}

func (w *wireAnalyzer) rejectReservedField(t types.Type, member wireMember, _ WireType, _ *FieldDescriptor) error {
	if w.typescript && isReservedFrontendField(member.Name) {
		return w.fail(t, "reserved frontend wire field %q", member.Name)
	}
	return nil
}

func (w *wireAnalyzer) applyPresence(t types.Type, member wireMember, wire WireType, field *FieldDescriptor) error {
	if !w.contract {
		return nil
	}
	presence, err := w.fieldPresence(t, member, wire)
	if err != nil {
		return err
	}
	field.Presence = presence
	return nil
}

func inputMissing(wire WireType) string {
	if wire.Kind == "optional" && wire.Contract.PointerDepth == 0 {
		return "missing"
	}
	return "zero"
}

func hasDynamicOmitZero(member wireMember, wire WireType) bool {
	return member.OmitZero && hasMethod(member.Type, "IsZero") && wire.Kind != "optional" && wire.Kind != "Date"
}

func newFieldPresence(member wireMember, wire WireType, parentNullable bool) *FieldPresence {
	presence := &FieldPresence{InputMissing: inputMissing(wire), InputNull: wire.Contract.InputNull, OutputNull: wire.Contract.OutputNull, OmitNil: nilableGo(member.Type), OmitMissing: wire.Kind == "optional" && wire.Contract.PointerDepth <= 1, OmitEmpty: member.OmitEmpty && emptyOmission(member.Type), OmitZero: member.OmitZero, EmbeddedParentNullable: parentNullable}
	presence.OutputRequired = !presence.OmitNil && !presence.OmitMissing && !presence.OmitEmpty && !presence.OmitZero && !parentNullable
	return presence
}

func (w *wireAnalyzer) fieldPresence(t types.Type, member wireMember, wire WireType) (*FieldPresence, error) {
	if hasDynamicOmitZero(member, wire) {
		return nil, w.fail(t, "field %s dynamic omitzero predicate requires a containing-type output schema declaration", member.Name)
	}
	presence := newFieldPresence(member, wire, embeddedNullable(t, member.Index))
	if !presence.OmitNil {
		return presence, nil
	}
	if err := w.resolveNilOutputNull(t, member, wire, presence); err != nil {
		return nil, err
	}
	return presence, nil
}

// resolveNilOutputNull records whether a nil-omitted field can still encode
// null. Only immediate nil is omitted, not a declared codec's encoded null.
// Nonnil pointers can also encode inner nil or Optional null.
func (w *wireAnalyzer) resolveNilOutputNull(t types.Type, member wireMember, wire WireType, presence *FieldPresence) error {
	presence.OutputNull = wire.Contract.Schemas != nil && schemaAcceptsNull(wire.Contract.Schemas.Output)
	pointer, ok := types.Unalias(member.Type).Underlying().(*types.Pointer)
	if !ok {
		return nil
	}
	element, err := w.describe(pointer.Elem())
	if err != nil {
		return fmt.Errorf("wire field %s.%s: %w", typeKey(t), member.Name, err)
	}
	presence.OutputNull = element.Contract.OutputNull
	return nil
}

func (w *wireAnalyzer) applySortable(t types.Type, member wireMember, wire WireType, field *FieldDescriptor) error {
	value := member.Tag.Get("sortable")
	if value == "" {
		return nil
	}
	if value != "true" && value != "false" {
		return w.fail(t, "sortable must be true or false")
	}
	field.Sortable = value == "true"
	if field.Sortable && (wire.Nullable || !isScalarWireKind(wire.Kind)) {
		return w.fail(t, "sortable field %s requires a declared comparable scalar", member.Name)
	}
	return nil
}

func applyIdentity(_ types.Type, member wireMember, _ WireType, field *FieldDescriptor) error {
	arcTags, err := modelshape.Options(member.Tag.Get("arc"))
	if err != nil {
		return err
	}
	for _, tag := range arcTags {
		if tag.Name == "identity" {
			field.Identity = true
		}
	}
	return nil
}

// ruleKind maps a wire kind to the kind vocabulary used by rule parsing.
func ruleKind(kind string) string {
	switch kind {
	case "enum":
		return "number"
	case "array":
		return "collection"
	case "string", "number", "boolean":
		return kind
	}
	return "object"
}

func (w *wireAnalyzer) applyRules(t types.Type, member wireMember, wire WireType, field *FieldDescriptor) error {
	rules, err := validation.ParseRules(member.Tag.Get("rules"), member.Name, ruleKind(wire.Kind))
	if err != nil {
		return w.fail(t, "field %s: %v", member.Name, err)
	}
	field.Rules = rules
	tags, err := validation.ParseTags(member.Tag.Get("validate"))
	if err != nil {
		return err
	}
	if !tags.Required {
		return nil
	}
	if len(rules) > 0 {
		return w.fail(t, "field %s mixes required annotation with explicit rules; declare the presence rule explicitly", member.Name)
	}
	name := "notNull"
	if wire.Kind == "string" {
		name = "notEmpty"
	}
	field.Rules = append(field.Rules, validation.RuleDescriptor{Property: member.Name, Name: name, Message: "The " + member.Name + " field is required.", Source: "annotation"})
	return nil
}

func (w *wireAnalyzer) applyQueryArgument(t types.Type, member wireMember, field *FieldDescriptor, seenArgs map[string]bool) error {
	if len(field.Rules) > 0 {
		field.QueryRules = queryRuleRepresentation(member.Type)
	}
	key := strings.ToLower(member.Name)
	if seenArgs[key] {
		return w.fail(t, "ambiguous query arguments %s", member.Name)
	}
	seenArgs[key] = true
	queryTags, err := metadata.ParseQueryTags(member.Tag.Get("query"))
	if err != nil {
		return err
	}
	if w.typescript && queryTags.PreservePresence {
		return w.fail(t, "query preservePresence requires an explicit presence-compatible client contract")
	}
	field.Required, field.HasDefault, field.Default = queryTags.Required, queryTags.HasDefault, queryTags.Default
	if err := w.applyQueryBindingAndDefault(t, member, field, queryTags.PreservePresence); err != nil {
		return err
	}
	if slices.Contains(reservedQueryParameters, key) {
		return w.fail(t, "reserved query parameter %s", member.Name)
	}
	return nil
}

func (w *wireAnalyzer) applyQueryBindingAndDefault(t types.Type, member wireMember, field *FieldDescriptor, preservePresence bool) error {
	if w.contract {
		binding, err := w.queryBinding(t, member, field, preservePresence)
		if err != nil {
			return err
		}
		field.Binding = binding
	}
	if !field.HasDefault {
		return nil
	}
	return w.validateQueryDefault(t, member, field)
}

func queryMissing(field *FieldDescriptor) string {
	if field.Required {
		return "error"
	}
	if field.HasDefault {
		return "default"
	}
	return "zero"
}

// queryEncoding reports how a builtin query value is spelled and, for
// fixed-size arrays, its length.
func queryEncoding(memberType types.Type) (string, *int64) {
	queryType := unwrapOptional(memberType)
	for {
		pointer, ok := types.Unalias(queryType).(*types.Pointer)
		if !ok {
			break
		}
		queryType = pointer.Elem()
	}
	if hasMethod(queryType, "UnmarshalText") {
		return "scalar-text", nil
	}
	switch collection := queryType.Underlying().(type) {
	case *types.Array:
		length := collection.Len()
		return "csv-or-json-array", &length
	case *types.Slice:
		return "csv-or-json-array", nil
	}
	return "scalar-text", nil
}

func (w *wireAnalyzer) queryBinding(t types.Type, member wireMember, field *FieldDescriptor, preservePresence bool) (*QueryBinding, error) {
	if err := validateBuiltinQueryType(member.Type, false); err != nil {
		return nil, w.fail(t, "query parameter %q: %v", member.Name, err)
	}
	encoding, fixedLength := queryEncoding(member.Type)
	return &QueryBinding{Reader: "builtin", Encoding: encoding, CaseInsensitive: true, DuplicateNames: "reject-case-variants", RepeatedValues: "comma-join", EmptyAsMissing: !preservePresence, NullAsMissing: !preservePresence, PreservePresence: preservePresence, Missing: queryMissing(field), FixedLength: fixedLength}, nil
}

func (w *wireAnalyzer) targetSizes(t types.Type) types.Sizes {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return nil
	}
	pkg := w.packages[named.Obj().Pkg().Path()]
	if pkg == nil {
		return nil
	}
	return pkg.TypesSizes
}

func (w *wireAnalyzer) validateQueryDefault(t types.Type, member wireMember, field *FieldDescriptor) error {
	sizes := w.targetSizes(t)
	if sizes == nil {
		return w.fail(t, "query defaults require target compiler sizes")
	}
	defaultType := member.Type
	if w.contract {
		defaultType = unwrapOptional(defaultType)
	}
	if err := validateGoQueryDefault(defaultType, field.Default, sizes); err != nil {
		return w.fail(t, "query parameter %q has unsupported server default: %v", member.Name, err)
	}
	return nil
}
