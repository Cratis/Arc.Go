// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package modelshape inspects data fields without invoking methods or getters.
package modelshape

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// MaxDepth bounds framework model traversals.
const MaxDepth = 64

// Field describes a readable JSON member. Each Fields call owns its slices.
type Field struct {
	Index                       []int
	Name                        string
	Type                        reflect.Type
	Tag                         reflect.StructTag
	Tagged, OmitEmpty, OmitZero bool
}

// CamelCase preserves initialisms, matching Arc serialization.
func CamelCase(name string) string {
	runes := []rune(name)
	if len(runes) == 0 || !unicode.IsUpper(runes[0]) || (len(runes) > 1 && unicode.IsUpper(runes[1])) {
		return name
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// Fields applies JSON visibility and embedded-member dominance in declaration
// order. It rejects direct duplicate names and unsupported JSON options.
func Fields(t reflect.Type) ([]Field, error) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct type")
	}
	var candidates []Field
	if err := collect(t, nil, make(map[reflect.Type]bool), &candidates); err != nil {
		return nil, err
	}
	var result []Field
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if seen[candidate.Name] {
			continue
		}
		seen[candidate.Name] = true
		best := candidate
		count := 0
		for _, other := range candidates {
			if other.Name != best.Name {
				continue
			}
			if len(other.Index) < len(best.Index) || len(other.Index) == len(best.Index) && other.Tagged && !best.Tagged {
				best, count = other, 1
			} else if len(other.Index) == len(best.Index) && other.Tagged == best.Tagged {
				count++
			}
		}
		if count > 1 {
			if len(best.Index) == 1 {
				return nil, fmt.Errorf("ambiguous JSON members %q and %q", best.Name, best.Name)
			}
			continue
		}
		result = append(result, best)
	}
	return result, nil
}

func collect(t reflect.Type, prefix []int, ancestors map[reflect.Type]bool, result *[]Field) error {
	if ancestors[t] {
		return nil
	}
	if len(prefix) >= MaxDepth {
		return fmt.Errorf("model field depth exceeds %d", MaxDepth)
	}
	ancestors[t] = true
	defer delete(ancestors, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		base := f.Type
		if base.Kind() == reflect.Pointer {
			base = base.Elem()
		}
		if !f.IsExported() && (!f.Anonymous || base.Kind() != reflect.Struct) {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")
		if tag[0] == "-" {
			continue
		}
		index := append(append([]int(nil), prefix...), i)
		if f.Anonymous && tag[0] == "" && base.Kind() == reflect.Struct {
			if err := collect(base, index, ancestors, result); err != nil {
				return err
			}
			continue
		}
		name := tag[0]
		if name == "" {
			name = CamelCase(f.Name)
		}
		entry := Field{Index: index, Name: name, Type: f.Type, Tag: f.Tag, Tagged: tag[0] != ""}
		for _, option := range tag[1:] {
			switch option {
			case "omitempty":
				entry.OmitEmpty = true
			case "omitzero":
				entry.OmitZero = true
			case "":
			default:
				return fmt.Errorf("unsupported JSON option %q", option)
			}
		}
		*result = append(*result, entry)
	}
	return nil
}

// Children returns direct exported graph nodes in declaration order. Unlike
// Fields it preserves embedded struct nodes so their validators are not skipped.
// An untagged embedded struct has an empty name: its descendants keep their
// promoted wire paths. Hidden fields and private representations are not walked.
func Children(t reflect.Type) ([]Field, error) {
	if _, err := Fields(t); err != nil {
		return nil, err
	}
	var children []Field
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		base := field.Type
		if base.Kind() == reflect.Pointer {
			base = base.Elem()
		}
		if name == "" && (!field.Anonymous || base.Kind() != reflect.Struct) {
			name = CamelCase(field.Name)
		}
		children = append(children, Field{Index: []int{i}, Name: name, Type: field.Type, Tag: field.Tag})
	}
	return children, nil
}

// Value reads a field without allocating nil embedded pointers.
func Value(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}

// Option is one name[=value] token. HasValue distinguishes an empty value.
type Option struct {
	Name, Value string
	HasValue    bool
}

// Options parses comma-separated name[=value] tokens. Backslash escapes only
// comma, equals and backslash. Whitespace is literal. Empty/duplicate names,
// empty tokens, extra unescaped equals and invalid escapes are rejected.
func Options(text string) ([]Option, error) {
	if text == "" {
		return nil, nil
	}
	var options []Option
	seen := make(map[string]bool)
	var name, value strings.Builder
	hasValue, escaped := false, false
	finish := func() error {
		key := name.String()
		if key == "" || strings.TrimSpace(key) != key || seen[key] {
			return fmt.Errorf("invalid or duplicate tag option %q", key)
		}
		seen[key] = true
		options = append(options, Option{Name: key, Value: value.String(), HasValue: hasValue})
		name.Reset()
		value.Reset()
		hasValue = false
		return nil
	}
	for _, c := range text {
		if escaped {
			if c != ',' && c != '=' && c != '\\' {
				return nil, fmt.Errorf("invalid tag escape")
			}
			escaped = false
		} else {
			switch c {
			case '\\':
				escaped = true
				continue
			case ',':
				if err := finish(); err != nil {
					return nil, err
				}
				continue
			case '=':
				if hasValue {
					return nil, fmt.Errorf("unescaped equals in tag value")
				}
				hasValue = true
				continue
			}
		}
		if hasValue {
			value.WriteRune(c)
		} else {
			name.WriteRune(c)
		}
	}
	if escaped {
		return nil, fmt.Errorf("unfinished tag escape")
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return options, nil
}
