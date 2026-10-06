// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
)

func (r *openAPIRenderer) model(node TypeDescriptor, direction string) (openAPIObject, error) {
	if node.Schemas != nil || node.Import != nil || node.Discriminator != nil || node.Base != "" || node.Interface != "" || node.DerivedID != "" || len(node.Derivatives) > 0 {
		return nil, fmt.Errorf("opaque and derived models are outside checkpoint profile")
	}
	switch node.Kind {
	case "model":
		return r.fields(node.Fields, direction, false)
	case "enum":
		if node.EnumDomain == "int32-parser" {
			return nil, fmt.Errorf("Int32 enum parser input semantics are outside checkpoint profile; OpenAPI publication is not supported")
		}
		if node.EnumDomain != "open-underlying-integer" || len(node.Members) == 0 || node.Scalar == nil || node.Scalar.Representation != "integer" {
			return nil, fmt.Errorf("enum requires its open integer domain and declared members")
		}
		schema, err := openAPIScalar(node.Scalar)
		if err != nil {
			return nil, err
		}
		members := slices.Clone(node.Members)
		slices.SortFunc(members, func(a, b EnumMember) int { return strings.Compare(a.Name, b.Name) })
		values := make([]openAPIObject, len(members))
		for i, member := range members {
			if member.Name == "" || i > 0 && member.Name == members[i-1].Name {
				return nil, fmt.Errorf("duplicate or empty enum member %q", member.Name)
			}
			value, err := openAPIInteger(member.Value)
			if err != nil {
				return nil, err
			}
			integer, _ := new(big.Int).SetString(member.Value, 10)
			minimum, _ := new(big.Int).SetString(node.Scalar.Minimum, 10)
			maximum, _ := new(big.Int).SetString(node.Scalar.Maximum, 10)
			if integer.Cmp(minimum) < 0 || integer.Cmp(maximum) > 0 {
				return nil, fmt.Errorf("enum member %s outside scalar domain", member.Name)
			}
			values[i] = openAPIObject{"name": member.Name, "value": value}
		}
		schema["x-cratis-enum"] = openAPIObject{"members": values, "flags": node.Flags, "domain": node.EnumDomain}
		return schema, nil
	default:
		return nil, fmt.Errorf("unsupported model kind %q", node.Kind)
	}
}

func openAPIInteger(value string) (json.Number, error) {
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.String() != value {
		return "", fmt.Errorf("invalid canonical integer token %q", value)
	}
	return json.Number(value), nil
}

func openAPIScalar(scalar *ScalarContract) (openAPIObject, error) {
	if scalar == nil || scalar.Format != "" || len(scalar.SpecialValues) != 0 {
		return nil, fmt.Errorf("missing scalar or unsupported format/special-value codec")
	}
	switch scalar.Representation {
	case "string", "boolean":
		if scalar.Bits != 0 || scalar.Unsigned || scalar.Minimum != "" || scalar.Maximum != "" {
			return nil, fmt.Errorf("nonnumeric scalar has numeric constraints")
		}
		return openAPIObject{"type": scalar.Representation}, nil
	case "integer":
		if scalar.Bits != 8 && scalar.Bits != 16 && scalar.Bits != 32 && scalar.Bits != 64 {
			return nil, fmt.Errorf("unsupported integer width %d", scalar.Bits)
		}
		minimum, err := openAPIInteger(scalar.Minimum)
		if err != nil {
			return nil, err
		}
		maximum, err := openAPIInteger(scalar.Maximum)
		if err != nil {
			return nil, err
		}
		bits := scalar.Bits
		if !scalar.Unsigned {
			bits--
		}
		upper := new(big.Int).Lsh(big.NewInt(1), uint(bits))
		lower := new(big.Int)
		if !scalar.Unsigned {
			lower.Neg(upper)
		}
		upper.Sub(upper, big.NewInt(1))
		if scalar.Minimum != lower.String() || scalar.Maximum != upper.String() {
			return nil, fmt.Errorf("integer bounds disagree with width/signedness")
		}
		return openAPIObject{"type": "integer", "minimum": minimum, "maximum": maximum}, nil
	default:
		return nil, fmt.Errorf("unsupported scalar representation %q", scalar.Representation)
	}
}

func (r *openAPIRenderer) wire(wire WireType, direction string, nullable bool, framework bool, depth int) (openAPIObject, error) {
	if depth > 128 {
		return nil, fmt.Errorf("wire nesting exceeds checkpoint budget")
	}
	contract := wire.Contract
	if contract == nil && !framework {
		return nil, fmt.Errorf("missing directional wire contract")
	}
	if contract != nil && (contract.Schemas != nil || contract.ByteArray || contract.ArrayInputLength != "" && contract.ArrayInputLength != "exact") {
		return nil, fmt.Errorf("declared codecs and byte-array binding are outside checkpoint profile")
	}
	var schema openAPIObject
	var err error
	switch wire.Kind {
	case "model", "enum":
		name := r.names[wire.Target]
		if name == "" {
			return nil, fmt.Errorf("unresolved type %q", wire.Target)
		}
		schema = openAPIRef(name + "." + direction)
	case "framework":
		if !framework || direction != "Output" || (wire.Target != "Cratis.ValidationResult" && wire.Target != "Cratis.PagingInfo") {
			return nil, fmt.Errorf("unsupported framework reference %q", wire.Target)
		}
		schema = openAPIRef(wire.Target)
	case "string", "boolean", "number":
		if contract == nil {
			if wire.Kind == "number" {
				return nil, fmt.Errorf("framework number requires exact scalar contract")
			}
			schema = openAPIObject{"type": wire.Kind}
		} else {
			schema, err = openAPIScalar(contract.Scalar)
			if err == nil && (wire.Kind == "number") != (contract.Scalar.Representation == "integer") {
				return nil, fmt.Errorf("wire kind disagrees with scalar representation")
			}
			if err == nil && wire.Kind != "number" && wire.Kind != contract.Scalar.Representation {
				return nil, fmt.Errorf("wire kind disagrees with scalar representation")
			}
		}
	case "Guid":
		// Framework correlation IDs are canonical UUIDs; application UUID codec
		// input spellings require their own witness and are not admitted here.
		if !framework {
			return nil, fmt.Errorf("application UUID codec requires a separate witnessed profile")
		}
		schema = openAPIObject{"type": "string", "format": "uuid"}
	case "array", "record", "optional":
		if wire.Element == nil {
			return nil, fmt.Errorf("%s has no element contract", wire.Kind)
		}
		elementNull := wire.Element.Nullable
		if wire.Element.Contract != nil {
			elementNull = wire.Element.Contract.OutputNull
			if direction == "Input" {
				elementNull = wire.Element.Contract.InputNull
			}
		}
		if wire.Kind == "optional" {
			// Property-level omission/null facts override the optional value's
			// nullability, just as they override pointer/ref nullability.
			elementNull = false
		}
		element, err := r.wire(*wire.Element, direction, elementNull, framework, depth+1)
		if err != nil {
			return nil, err
		}
		switch wire.Kind {
		case "optional":
			schema = element
		case "array":
			schema = openAPIObject{"type": "array", "items": element}
			if contract != nil && contract.FixedLength != nil {
				if *contract.FixedLength < 0 {
					return nil, fmt.Errorf("negative fixed array length")
				}
				schema["minItems"], schema["maxItems"] = *contract.FixedLength, *contract.FixedLength
			}
		case "record":
			schema = openAPIObject{"type": "object", "additionalProperties": element}
		}
	default:
		return nil, fmt.Errorf("unsupported wire kind %q", wire.Kind)
	}
	if err != nil {
		return nil, err
	}
	if contract != nil && contract.Concept != "" {
		schema["x-cratis-concept"] = contract.Concept
	}
	return openAPINull(schema, nullable), nil
}

func (r *openAPIRenderer) fields(fields []FieldDescriptor, direction string, framework bool) (openAPIObject, error) {
	ordered := slices.Clone(fields)
	slices.SortFunc(ordered, func(a, b FieldDescriptor) int { return strings.Compare(a.Name, b.Name) })
	properties := openAPIObject{}
	required := []string{}
	for _, field := range ordered {
		if field.Name == "" || properties[field.Name] != nil || field.Presence == nil || field.Binding != nil || field.HasDefault || field.Required {
			return nil, fmt.Errorf("field %q requires unique JSON name and directional presence, without query binding", field.Name)
		}
		nullable, mandatory := field.Presence.OutputNull, field.Presence.OutputRequired
		if direction == "Input" {
			nullable, mandatory = field.Presence.InputNull, field.Presence.InputRequired
		}
		schema, err := r.wire(field.Type, direction, nullable, framework, 0)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		if len(field.Rules) > 0 {
			return nil, fmt.Errorf("field %s: validation rules need a witnessed metadata projection", field.Name)
		}
		properties[field.Name] = schema
		if mandatory {
			required = append(required, field.Name)
		}
	}
	schema := openAPIObject{"type": "object", "properties": properties}
	if direction == "Output" {
		// The admitted builtin codec emits exactly the selected model fields;
		// opaque codecs and extension-producing derived models were refused.
		schema["additionalProperties"] = false
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, nil
}
