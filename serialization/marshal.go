// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

// Marshal encodes Arc camelCase structs, retaining zeros and omitting nil object
// properties. Nil elements in collections remain null. Custom json.Marshaler
// implementations own their wire format. String-key maps retain null entries.
// Embedded fields and non-string map keys need a custom codec. Nesting is limited
// to 64 levels; cycles fail rather than recursing indefinitely. No input is retained.
func Marshal(value any) ([]byte, error) { return marshal(reflect.ValueOf(value), 0) }

func marshal(v reflect.Value, depth int) ([]byte, error) {
	if depth > 64 {
		return nil, fmt.Errorf("JSON nesting exceeds 64 levels (possibly cyclic)")
	}
	if nilValue(v) {
		return []byte("null"), nil
	}
	if v.CanInterface() {
		if optional, ok := v.Interface().(interface{ optionalValue() (any, bool, bool) }); ok {
			value, present, null := optional.optionalValue()
			if !present || null {
				return []byte("null"), nil
			}
			return marshal(reflect.ValueOf(value), depth+1)
		}
		if custom, ok := v.Interface().(json.Marshaler); ok {
			return json.Marshal(custom)
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return marshal(v.Elem(), depth+1)
	case reflect.Struct:
		return marshalStruct(v, depth)
	case reflect.Map:
		return marshalMap(v, depth)
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return json.Marshal(v.Interface())
		}
		items := make([]json.RawMessage, v.Len())
		for i := range items {
			data, err := marshal(v.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
			items[i] = data
		}
		return json.Marshal(items)
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) {
			return []byte(`"NaN"`), nil
		}
		if math.IsInf(f, 1) {
			return []byte(`"Infinity"`), nil
		}
		if math.IsInf(f, -1) {
			return []byte(`"-Infinity"`), nil
		}
	}
	return json.Marshal(v.Interface())
}

func marshalStruct(v reflect.Value, depth int) ([]byte, error) {
	members, err := fields(v.Type())
	if err != nil {
		return nil, err
	}
	object := make(map[string]json.RawMessage, len(members))
	for _, f := range members {
		value := v.Field(f.index)
		if nilValue(value) || f.omitEmpty && emptyValue(value) || f.omitZero && value.IsZero() {
			continue
		}
		if optional, ok := value.Interface().(interface{ optionalValue() (any, bool, bool) }); ok {
			if _, present, _ := optional.optionalValue(); !present {
				continue
			}
		}
		data, err := marshal(value, depth+1)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", f.name, err)
		}
		object[f.name] = data
	}
	return json.Marshal(object)
}

func marshalMap(v reflect.Value, depth int) ([]byte, error) {
	if v.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("non-string dictionary keys require a custom JSON codec")
	}
	object := make(map[string]json.RawMessage, v.Len())
	iterator := v.MapRange()
	for iterator.Next() {
		data, err := marshal(iterator.Value(), depth+1)
		if err != nil {
			return nil, err
		}
		object[iterator.Key().String()] = data
	}
	return json.Marshal(object)
}
