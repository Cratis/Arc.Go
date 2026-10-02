// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
)

// DuplicateMemberError identifies ambiguous supplied names for one declared field.
// Use errors.As to inspect it. Unknown properties are ignored, including duplicates.
type DuplicateMemberError struct {
	// Member is the declared wire name, not the supplied value.
	Member string
}

// Error describes the rejected member without exposing its value.
func (e *DuplicateMemberError) Error() string {
	return fmt.Sprintf("duplicate JSON member %q", e.Member)
}

// Unmarshal binds one complete JSON value into a non-nil pointer. It starts with a
// fresh value and only replaces the target after success. Struct names are matched
// exactly, unknown members ignored, and exact duplicate declared keys rejected.
// Optional preserves missing/null/zero. Non-nullable scalars reject null. Custom
// JSON and text unmarshaler implementations own their binding rules. Untyped numbers become
// json.Number to preserve integer precision. Inputs are not retained.
func Unmarshal(data []byte, target any) error {
	v := reflect.ValueOf(target)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("JSON target must be a non-nil pointer")
	}
	if err := ValidateType(v.Elem().Type()); err != nil {
		return err
	}
	if err := validateJSON(data); err != nil {
		return err
	}
	fresh := reflect.New(v.Elem().Type()).Elem()
	if err := unmarshal(bytes.TrimSpace(data), fresh, 0); err != nil {
		return err
	}
	v.Elem().Set(fresh)
	return nil
}

func unmarshal(data []byte, v reflect.Value, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds 64 levels")
	}
	if err := ValidateType(v.Type()); err != nil {
		return err
	}
	if v.CanAddr() && v.Addr().CanInterface() {
		if optional, ok := v.Addr().Interface().(interface{ bindOptional([]byte, int) error }); ok {
			return optional.bindOptional(data, depth)
		}
		if custom, ok := v.Addr().Interface().(json.Unmarshaler); ok {
			return custom.UnmarshalJSON(data)
		}
		if _, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok {
			// encoding/json invokes text codecs for strings, ignores null, and rejects other tokens.
			return json.Unmarshal(data, v.Addr().Interface())
		}
	}
	if bytes.Equal(data, []byte("null")) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
			return nil
		}
		return fmt.Errorf("null requires a nullable target")
	}
	switch v.Kind() {
	case reflect.Interface:
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		return decoder.Decode(v.Addr().Interface())
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		return unmarshal(data, v.Elem(), depth+1)
	case reflect.Struct:
		return unmarshalStruct(data, v, depth)
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return json.Unmarshal(data, v.Addr().Interface())
		}
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		if v.Kind() == reflect.Array && len(values) != v.Len() {
			return fmt.Errorf("array length mismatch")
		}
		if v.Kind() == reflect.Slice {
			v.Set(reflect.MakeSlice(v.Type(), len(values), len(values)))
		}
		for i, value := range values {
			if err := unmarshal(value, v.Index(i), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		return unmarshalMap(data, v, depth)
	case reflect.Float32, reflect.Float64:
		if string(data) == `"NaN"` || string(data) == `"Infinity"` || string(data) == `"-Infinity"` {
			f, err := strconv.ParseFloat(string(data[1:len(data)-1]), v.Type().Bits())
			if err != nil {
				return err
			}
			v.SetFloat(f)
			return nil
		}
	}
	return json.Unmarshal(data, v.Addr().Interface())
}

func unmarshalStruct(data []byte, v reflect.Value, depth int) error {
	members, err := fields(v.Type())
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("expected JSON object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return fmt.Errorf("expected JSON member name")
		}
		var raw json.RawMessage
		if err = decoder.Decode(&raw); err != nil {
			return err
		}
		for _, f := range members {
			if name != f.name {
				continue
			}
			if seen[f.name] {
				return &DuplicateMemberError{Member: f.name}
			}
			seen[f.name] = true
			value, fieldErr := fieldValue(v, f.index, true)
			if fieldErr != nil {
				return fieldErr
			}
			if err = unmarshal(raw, value, depth+1); err != nil {
				return fmt.Errorf("bind %s: %w", f.name, err)
			}
			break
		}
	}
	_, err = decoder.Token()
	return err
}

func unmarshalMap(data []byte, v reflect.Value, depth int) error {
	if v.Type().Key().Kind() != reflect.String {
		return fmt.Errorf("non-string dictionary keys require a custom JSON codec")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	v.Set(reflect.MakeMapWithSize(v.Type(), len(values)))
	for key, data := range values {
		value := reflect.New(v.Type().Elem()).Elem()
		if err := unmarshal(data, value, depth+1); err != nil {
			return err
		}
		v.SetMapIndex(reflect.ValueOf(key).Convert(v.Type().Key()), value)
	}
	return nil
}
