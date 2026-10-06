// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"reflect"
	"strings"
)

// visitType rejects inline fields throughout reachable static filter types,
// including absent pointer/container values. It does not describe or encode
// structs. Ordinary recursive types are allowed; visit separately guards values.
func (g *filterGuard) visitType(t reflect.Type, depth int) error {
	if depth > maxFilterDepth {
		return ErrValue
	}
	if err := g.charge(1); err != nil {
		return err
	}
	if g.types[t] {
		return nil
	}
	g.types[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Array, reflect.Slice, reflect.Map:
		return g.visitType(t.Elem(), depth+1)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			if err := g.charge(len(field.Name) + len(field.Tag)); err != nil {
				return err
			}
			if filterFieldInline(field) {
				return &operationError{"inline filter field is unsupported", ErrValue}
			}
			if err := g.visitType(field.Type, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// Match the pinned driver's default BSON tag parser, not JSON fallback tags.
// Its legacy bare tag syntax and first-token "inline" also activate inlining;
// only the entire tag "-" skips a field ("-,inline" still activates it).
func filterFieldInline(field reflect.StructField) bool {
	tag, ok := field.Tag.Lookup("bson")
	if !ok && !strings.Contains(string(field.Tag), ":") {
		tag = string(field.Tag)
	}
	for token := range strings.SplitSeq(tag, ",") {
		if token == "inline" {
			return true
		}
	}
	return false
}
