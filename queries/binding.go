// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
	fconcepts "github.com/cratis/fundamentals.go/concepts"
)

// Parameter describes a caller input, never a service parameter.
type Parameter struct {
	Name       string
	Type       reflect.Type
	Required   bool
	HasDefault bool
}

// ArgumentOptions defines explicit presence, default and custom conversion rules.
// DecodeText/DecodeJSON run only during binding, not Build. Defaults are typed values.
type ArgumentOptions[T any] struct {
	Required         bool
	Default          serialization.Optional[T]
	PreservePresence bool
	DecodeText       func(string) (T, error)
	DecodeJSON       func([]byte) (T, error)
}

// Argument is an immutable typed binding. Zero is invalid.
type Argument[A any] struct {
	parameter    Parameter
	preserve     bool
	hasDefault   bool
	defaultValue reflect.Value
	decode       func(any, provenance) (reflect.Value, error)
	set          func(*A, reflect.Value)
	explicit     bool
	err          error
}

// BindArgument supplies custom/generated binding; names replace matching compiled fields.
// JSON-node collections require this explicit escape hatch and DecodeJSON.
func BindArgument[A, T any](name string, set func(*A, T), o ArgumentOptions[T]) Argument[A] {
	t := reflect.TypeFor[T]()
	a := Argument[A]{parameter: Parameter{Name: name, Type: t, Required: o.Required, HasDefault: o.Default.IsPresent()}, preserve: o.PreservePresence, hasDefault: o.Default.IsPresent(), explicit: true}
	if set == nil || name == "" || reserved(name) || o.Required && a.hasDefault {
		a.err = ErrInvalidArguments
		return a
	}
	if a.hasDefault {
		v, present := o.Default.Value()
		if !present {
			a.err = ErrInvalidArguments
			return a
		}
		a.defaultValue = reflect.ValueOf(&v).Elem()
	}
	if o.DecodeText == nil && o.DecodeJSON == nil {
		a.err = validateInputType(t, false)
	}
	a.decode = func(raw any, source provenance) (reflect.Value, error) {
		if source == getInput && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && t.Elem() == reflect.TypeFor[json.RawMessage]() {
			return reflect.Value{}, ErrInvalidArguments
		}
		if o.DecodeText != nil {
			if text, ok := rawText(raw, source); ok {
				v, e := o.DecodeText(text)
				return reflect.ValueOf(&v).Elem(), e
			}
		}
		if o.DecodeJSON != nil {
			data, e := rawJSON(raw)
			if e != nil {
				return reflect.Value{}, e
			}
			v, e := o.DecodeJSON(data)
			return reflect.ValueOf(&v).Elem(), e
		}
		return convertRaw(raw, t, source, false)
	}
	a.set = func(target *A, v reflect.Value) { set(target, v.Interface().(T)) }
	return a
}

func compileArguments[A any](explicit []Argument[A]) ([]Argument[A], []Parameter, error) {
	t := reflect.TypeFor[A]()
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return nil, nil, ErrInvalidArguments
	}
	if err := serialization.ValidateType(t); err != nil {
		return nil, nil, err
	}
	fields, err := modelshape.Fields(t)
	if err != nil {
		return nil, nil, err
	}
	overrides := map[string]Argument[A]{}
	for _, a := range explicit {
		key := strings.ToLower(a.parameter.Name)
		if a.err != nil {
			return nil, nil, a.err
		}
		if a.set == nil || key == "" || reserved(key) {
			return nil, nil, ErrInvalidArguments
		}
		if _, ok := overrides[key]; ok {
			return nil, nil, ErrDuplicate
		}
		overrides[key] = a
	}
	var bindings []Argument[A]
	seen := map[string]bool{}
	for _, field := range fields {
		if _, err := validation.ParseTags(field.Tag.Get("validate")); err != nil {
			return nil, nil, err
		}
		key := strings.ToLower(field.Name)
		if seen[key] || reserved(key) {
			return nil, nil, ErrInvalidArguments
		}
		seen[key] = true
		if a, ok := overrides[key]; ok {
			bindings = append(bindings, a)
			delete(overrides, key)
			continue
		}
		if err := validateInputType(field.Type, false); err != nil {
			return nil, nil, fmt.Errorf("argument %s: %w", field.Name, err)
		}
		tags, err := metadata.ParseQueryTags(field.Tag.Get("query"))
		if err != nil {
			return nil, nil, err
		}
		a := Argument[A]{parameter: Parameter{Name: field.Name, Type: field.Type, Required: tags.Required, HasDefault: tags.HasDefault}, preserve: tags.PreservePresence, hasDefault: tags.HasDefault}
		if tags.HasDefault {
			a.defaultValue, err = convertRaw(tags.Default, field.Type, directInput, false)
			if err != nil {
				return nil, nil, err
			}
		}
		a.decode = func(raw any, source provenance) (reflect.Value, error) {
			return convertRaw(raw, field.Type, source, false)
		}
		index := append([]int(nil), field.Index...)
		a.set = func(target *A, value reflect.Value) {
			v := reflect.ValueOf(target).Elem()
			for _, i := range index {
				if v.Kind() == reflect.Pointer {
					if v.IsNil() {
						v.Set(reflect.New(v.Type().Elem()))
					}
					v = v.Elem()
				}
				v = v.Field(i)
			}
			v.Set(value)
		}
		bindings = append(bindings, a)
	}
	// Preserve explicit declaration order for additional setter-only arguments.
	for _, a := range explicit {
		if _, ok := overrides[strings.ToLower(a.parameter.Name)]; ok {
			bindings = append(bindings, a)
		}
	}
	parameters := make([]Parameter, len(bindings))
	for i, a := range bindings {
		parameters[i] = a.parameter
	}
	return bindings, parameters, nil
}
func bindArguments[A any](request Request, bindings []Argument[A]) (any, error) {
	if request.hasTyped {
		value, ok := request.typed.(A)
		if !ok {
			return nil, ErrInvalidArguments
		}
		return value, nil
	}
	var target A
	var failures []error
	for _, a := range bindings {
		raw, present := request.arguments.Get(a.parameter.Name)
		effective := present
		if present && !a.preserve && omittedRaw(raw, request.arguments.source) {
			if request.arguments.source != directInput || !representsEmptyString(a.parameter.Type) {
				effective = false
			}
		}
		if !effective {
			if a.hasDefault {
				data, err := serialization.Marshal(a.defaultValue.Interface())
				value := reflect.New(a.parameter.Type)
				if err == nil {
					err = serialization.Unmarshal(data, value.Interface())
				}
				if err != nil {
					failures = append(failures, &ArgumentError{Name: a.parameter.Name, Type: a.parameter.Type, Cause: err})
					continue
				}
				a.set(&target, value.Elem())
				continue
			}
			if a.parameter.Required {
				failures = append(failures, &ArgumentError{Name: a.parameter.Name, Type: a.parameter.Type, Missing: true})
			}
			continue
		}
		value, err := a.decode(raw, request.arguments.source)
		if err != nil {
			failures = append(failures, &ArgumentError{Name: a.parameter.Name, Type: a.parameter.Type, Cause: err})
			continue
		}
		if a.parameter.Required && nullRaw(raw) {
			failures = append(failures, &ArgumentError{Name: a.parameter.Name, Type: a.parameter.Type, Missing: true})
			continue
		}
		a.set(&target, value)
	}
	if len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return target, nil
}
func nullRaw(raw any) bool {
	if raw == nil {
		return true
	}
	if b, ok := raw.(json.RawMessage); ok {
		return bytes.Equal(bytes.TrimSpace(b), []byte("null"))
	}
	return false
}
func omittedRaw(raw any, source provenance) bool {
	if nullRaw(raw) {
		return true
	}
	if text, ok := raw.(string); ok {
		return text == ""
	}
	if values, ok := raw.([]string); ok {
		return strings.Join(values, ",") == ""
	}
	if source == queryInput {
		if b, ok := raw.(json.RawMessage); ok {
			var text string
			if json.Unmarshal(b, &text) == nil {
				return text == ""
			}
		}
	}
	return false
}
func representsEmptyString(t reflect.Type) bool {
	if base, ok := optionalElement(t); ok {
		return representsEmptyString(base)
	}
	if t.Kind() == reflect.Pointer {
		return representsEmptyString(t.Elem())
	}
	if base, recognized, err := fconcepts.Underlying(t); err == nil && recognized {
		return base.Type.Kind() == reflect.String
	}
	return t.Kind() == reflect.String
}

func optionalElement(t reflect.Type) (reflect.Type, bool) {
	// Inspect only framework Optional's type metadata, never its private value.
	if t.PkgPath() == "github.com/cratis/arc.go/serialization" && strings.HasPrefix(t.Name(), "Optional[") {
		return t.Field(0).Type, true
	}
	return nil, false
}
func validateInputType(t reflect.Type, collection bool) error {
	if base, ok := optionalElement(t); ok {
		return validateInputType(base, collection)
	}
	if _, recognized, err := fconcepts.Underlying(t); err != nil {
		return err
	} else if recognized {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		return validateInputType(t.Elem(), collection)
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return ErrInvalidArguments
	}
	if reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		return nil
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return nil
	case reflect.Slice, reflect.Array:
		if collection {
			return ErrInvalidArguments
		}
		return validateInputType(t.Elem(), true)
	}
	return ErrInvalidArguments
}
func rawText(raw any, source provenance) (string, bool) {
	switch v := raw.(type) {
	case string:
		return v, true
	case json.RawMessage:
		var text string
		if json.Unmarshal(v, &text) == nil {
			return text, true
		}
		trimmed := bytes.TrimSpace(v)
		if len(trimmed) > 0 && trimmed[0] != '{' && trimmed[0] != '[' && string(trimmed) != "null" {
			return string(trimmed), true
		}
	}
	return "", false
}
func rawJSON(raw any) ([]byte, error) {
	if b, ok := raw.(json.RawMessage); ok {
		return b, nil
	}
	return serialization.Marshal(raw)
}
func convertRaw(raw any, t reflect.Type, source provenance, collection bool) (reflect.Value, error) {
	if nullRaw(raw) {
		if t.Kind() == reflect.Pointer {
			return reflect.Zero(t), nil
		}
		if _, ok := optionalElement(t); ok {
			v := reflect.New(t)
			err := serialization.Unmarshal([]byte("null"), v.Interface())
			return v.Elem(), err
		}
		return reflect.Value{}, ErrInvalidArguments
	}
	if base, ok := optionalElement(t); ok {
		value, err := convertRaw(raw, base, source, collection)
		if err != nil {
			return reflect.Value{}, err
		}
		data, err := serialization.Marshal(value.Interface())
		if err != nil {
			return reflect.Value{}, err
		}
		v := reflect.New(t)
		err = serialization.Unmarshal(data, v.Interface())
		return v.Elem(), err
	}
	if t.Kind() == reflect.Pointer {
		value, err := convertRaw(raw, t.Elem(), source, collection)
		if err != nil {
			return reflect.Value{}, err
		}
		v := reflect.New(t.Elem())
		v.Elem().Set(value)
		return v, nil
	}
	if source == directInput && raw != nil && reflect.TypeOf(raw) == t {
		return reflect.ValueOf(raw), nil
	}
	// Scalar codecs (notably UUID's array representation) take precedence over collections.
	codecRaw := raw
	if values, ok := raw.([]string); ok {
		codecRaw = strings.Join(values, ",")
	}
	if text, ok := rawText(codecRaw, source); ok {
		v := reflect.New(t)
		if unmarshaler, ok := v.Interface().(encoding.TextUnmarshaler); ok {
			err := unmarshaler.UnmarshalText([]byte(text))
			return v.Elem(), err
		}
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		if collection {
			return reflect.Value{}, ErrInvalidArguments
		}
		var elements []any
		if b, ok := raw.(json.RawMessage); ok && len(bytes.TrimSpace(b)) > 0 && bytes.TrimSpace(b)[0] == '[' {
			var nodes []json.RawMessage
			if err := json.Unmarshal(b, &nodes); err != nil {
				return reflect.Value{}, err
			}
			for _, node := range nodes {
				elements = append(elements, node)
			}
		} else {
			var texts []string
			switch v := raw.(type) {
			case []string:
				texts = v
			case string:
				texts = []string{v}
			case json.RawMessage:
				var text string
				if err := json.Unmarshal(v, &text); err != nil {
					return reflect.Value{}, err
				}
				texts = []string{text}
			default:
				return reflect.Value{}, ErrInvalidArguments
			}
			for _, text := range texts {
				for _, part := range strings.Split(text, ",") {
					elements = append(elements, strings.TrimSpace(part))
				}
			}
		}
		var v reflect.Value
		if t.Kind() == reflect.Array {
			if len(elements) != t.Len() {
				return reflect.Value{}, ErrInvalidArguments
			}
			v = reflect.New(t).Elem()
		} else {
			v = reflect.MakeSlice(t, len(elements), len(elements))
		}
		for i, element := range elements {
			value, err := convertRaw(element, t.Elem(), source, true)
			if err != nil {
				return reflect.Value{}, err
			}
			v.Index(i).Set(value)
		}
		return v, nil
	}
	if values, ok := raw.([]string); ok {
		raw = strings.Join(values, ",")
	}
	text, ok := rawText(raw, source)
	if !ok {
		return reflect.Value{}, ErrInvalidArguments
	}
	v := reflect.New(t)
	if unmarshaler, ok := v.Interface().(encoding.TextUnmarshaler); ok {
		err := unmarshaler.UnmarshalText([]byte(text))
		return v.Elem(), err
	}
	value := v.Elem()
	var err error
	switch t.Kind() {
	case reflect.String:
		value.SetString(text)
	case reflect.Bool:
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true":
			value.SetBool(true)
		case "false":
			value.SetBool(false)
		default:
			err = &strconv.NumError{Func: "ParseBool", Num: text, Err: strconv.ErrSyntax}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var x int64
		x, err = strconv.ParseInt(text, 10, t.Bits())
		value.SetInt(x)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var x uint64
		x, err = strconv.ParseUint(text, 10, t.Bits())
		value.SetUint(x)
	case reflect.Float32, reflect.Float64:
		var x float64
		x, err = strconv.ParseFloat(text, t.Bits())
		value.SetFloat(x)
	default:
		err = ErrInvalidArguments
	}
	return value, err
}
