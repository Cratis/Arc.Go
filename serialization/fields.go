// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// CamelCase uses Arc's acronym-friendly policy: ID and URLValue remain unchanged;
// ordinary PascalCase names become camelCase. Explicit json tags take precedence.
func CamelCase(name string) string {
	runes := []rune(name)
	if len(runes) == 0 || !unicode.IsUpper(runes[0]) || (len(runes) > 1 && unicode.IsUpper(runes[1])) {
		return name
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

type field struct {
	index     int
	name      string
	omitEmpty bool
	omitZero  bool
}

func fields(t reflect.Type) ([]field, error) {
	var result []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")
		if tag[0] == "-" {
			continue
		}
		if f.Anonymous {
			return nil, fmt.Errorf("embedded field %s requires a custom JSON codec", f.Name)
		}
		name := tag[0]
		if name == "" {
			name = CamelCase(f.Name)
		}
		entry := field{index: i, name: name}
		for _, option := range tag[1:] {
			switch option {
			case "omitempty":
				entry.omitEmpty = true
			case "omitzero":
				entry.omitZero = true
			case "":
			default:
				return nil, fmt.Errorf("unsupported JSON option %q", option)
			}
		}
		for _, other := range result {
			if strings.EqualFold(other.name, name) {
				return nil, fmt.Errorf("ambiguous JSON members %q and %q", other.name, name)
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

func nilValue(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Interface:
		return v.IsNil() || nilValue(v.Elem())
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return v.IsNil()
	}
	return false
}

func emptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	}
	return v.IsZero()
}
