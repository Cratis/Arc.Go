// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
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
	index     []int
	name      string
	tagged    bool
	omitEmpty bool
	omitZero  bool
}

type fieldPlan struct {
	once    sync.Once
	members []field
	err     error
}

var fieldPlans sync.Map // reflect.Type -> *fieldPlan

func fields(t reflect.Type) ([]field, error) {
	entry, loaded := fieldPlans.Load(t)
	if !loaded {
		entry, _ = fieldPlans.LoadOrStore(t, &fieldPlan{})
	}
	plan := entry.(*fieldPlan)
	plan.once.Do(func() { plan.members, plan.err = buildFields(t) })
	return plan.members, plan.err
}

func buildFields(t reflect.Type) ([]field, error) {
	var candidates []field
	if err := collectFields(t, nil, make(map[reflect.Type]bool), &candidates); err != nil {
		return nil, err
	}
	var result []field
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if seen[candidate.name] {
			continue
		}
		seen[candidate.name] = true
		best := candidate
		count := 0
		for _, other := range candidates {
			if other.name != best.name {
				continue
			}
			if len(other.index) < len(best.index) || len(other.index) == len(best.index) && other.tagged && !best.tagged {
				best = other
				count = 1
			} else if len(other.index) == len(best.index) && other.tagged == best.tagged {
				count++
			}
		}
		if count > 1 {
			if len(best.index) == 1 {
				return nil, fmt.Errorf("ambiguous JSON members %q and %q", best.name, best.name)
			}
			// Equally dominant promoted fields cancel each other, like encoding/json.
			continue
		}
		result = append(result, best)
	}
	return result, nil
}

func collectFields(t reflect.Type, prefix []int, ancestors map[reflect.Type]bool, result *[]field) error {
	if ancestors[t] {
		return nil
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
			if err := collectFields(base, index, ancestors, result); err != nil {
				return err
			}
			continue
		}
		name := tag[0]
		if name == "" {
			name = CamelCase(f.Name)
		}
		entry := field{index: index, name: name, tagged: tag[0] != ""}
		for _, option := range tag[1:] {
			switch option {
			case "omitempty":
				entry.omitEmpty = true
			case "omitzero":
				entry.omitZero = true
			case "":
			default:
				return fmt.Errorf("unsupported JSON option %q", option)
			}
		}
		*result = append(*result, entry)
	}
	return nil
}

func fieldValue(v reflect.Value, index []int, allocate bool) (reflect.Value, error) {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !allocate {
					return reflect.Value{}, nil
				}
				if !v.CanSet() {
					return reflect.Value{}, fmt.Errorf("cannot allocate unexported embedded pointer %s", v.Type())
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v, nil
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
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

func zeroValue(v reflect.Value) bool {
	if nilValue(v) {
		return true
	}
	zeroer := reflect.TypeFor[interface{ IsZero() bool }]()
	if v.CanInterface() && v.Type().Implements(zeroer) {
		return v.Interface().(interface{ IsZero() bool }).IsZero()
	}
	if v.Kind() != reflect.Pointer && reflect.PointerTo(v.Type()).Implements(zeroer) {
		if !v.CanAddr() {
			// encoding/json boxes unaddressable values for pointer-receiver IsZero.
			copy := reflect.New(v.Type()).Elem()
			copy.Set(v)
			v = copy
		}
		if v.Addr().CanInterface() {
			return v.Addr().Interface().(interface{ IsZero() bool }).IsZero()
		}
	}
	return v.IsZero()
}
