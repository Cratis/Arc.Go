// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/x/bsonx/bsoncore"
)

// These limits apply to every instruction boundary, including opaque Arc JSON
// detachment. The graph budget counts nodes and string/byte payloads, not BSON
// output; the writer independently enforces the exact encoded byte budget.
const maxFilterBytes = 16 << 20
const maxFilterDepth = 64

type filterIdentity struct {
	typeOf  reflect.Type
	pointer uintptr
}

type filterGuard struct {
	remaining int
	active    map[filterIdentity]bool
}

func guardFilter(filter bson.D, limit int) error {
	g := filterGuard{remaining: maxFilterBytes, active: make(map[filterIdentity]bool)}
	return g.visit(reflect.ValueOf(filter), 0, limit)
}

func (g *filterGuard) charge(n int) error {
	if n > g.remaining {
		return ErrLimit
	}
	g.remaining -= n
	return nil
}

func (g *filterGuard) visit(v reflect.Value, depth, limit int) error {
	if depth > maxFilterDepth {
		return ErrValue
	}
	if err := g.charge(1); err != nil {
		return err
	}
	if !v.IsValid() {
		return nil
	}
	// Only known driver scalar types with private state are exempt from field
	// traversal. No application hook is executed to discover its representation.
	if v.Type() == reflect.TypeFor[time.Time]() || v.Type() == reflect.TypeFor[bson.Decimal128]() || v.Type() == reflect.TypeFor[bson.ObjectID]() {
		return nil
	}
	if v.CanInterface() {
		switch raw := v.Interface().(type) {
		case bson.Raw:
			if err := g.charge(len(raw)); err != nil {
				return err
			}
			return guardRawDocument(raw, false, depth, limit)
		case bsoncore.Document:
			if err := g.charge(len(raw)); err != nil {
				return err
			}
			return guardRawDocument(raw, false, depth, limit)
		case bsoncore.Array:
			if err := g.charge(len(raw)); err != nil {
				return err
			}
			return guardRawDocument(raw, true, depth, limit)
		case bson.RawValue:
			if err := g.charge(len(raw.Value)); err != nil {
				return err
			}
			return guardRawValue(bsoncore.Value{Type: bsoncore.Type(raw.Type), Data: raw.Value}, depth, limit)
		}
	}
	base := v.Type()
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	known := base == reflect.TypeFor[bson.D]() || base == reflect.TypeFor[bson.DateTime]() ||
		base == reflect.TypeFor[bson.ObjectID]() || base == reflect.TypeFor[bson.Decimal128]() ||
		base == reflect.TypeFor[time.Time]() || base == reflect.TypeFor[bson.Raw]()
	if base == reflect.TypeFor[bson.Vector]() || (!known && hasCustomCodec(v.Type())) {
		return &operationError{"filter encoding hook is unsupported", ErrValue}
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Map || v.Kind() == reflect.Slice {
		if v.IsNil() {
			return nil
		}
		var pointer uintptr
		if v.Kind() == reflect.Map {
			pointer = uintptr(v.UnsafePointer())
		} else {
			pointer = v.Pointer()
		}
		id := filterIdentity{v.Type(), pointer}
		if g.active[id] {
			return ErrValue
		}
		g.active[id] = true
		defer delete(g.active, id)
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return g.visit(v.Elem(), depth+1, limit)
	case reflect.String:
		return g.charge(v.Len())
	case reflect.Array, reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return g.charge(v.Len())
		}
		if v.Len() > g.remaining {
			return ErrLimit
		}
		for i := 0; i < v.Len(); i++ {
			if err := g.visit(v.Index(i), depth+1, limit); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String || v.Len() > g.remaining {
			return ErrValue
		}
		it := v.MapRange()
		for it.Next() {
			if err := g.charge(it.Key().Len()); err != nil {
				return err
			}
			if err := g.visit(it.Value(), depth+1, limit); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if err := g.charge(len(field.Name) + len(field.Tag)); err != nil {
				return err
			}
			if err := g.visit(v.Field(i), depth+1, limit); err != nil {
				return err
			}
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
	default:
		return ErrValue
	}
	return nil
}

func guardRawDocument(raw []byte, array bool, depth, limit int) error {
	if len(raw) > limit {
		return ErrLimit
	}
	if depth > maxFilterDepth || len(raw) < 5 {
		return ErrValue
	}
	size, rest, ok := bsoncore.ReadLength(raw)
	if !ok || int64(size) != int64(len(raw)) || raw[len(raw)-1] != 0 {
		return ErrValue
	}
	rest = rest[:len(rest)-1]
	index := 0
	for len(rest) > 0 {
		element, next, ok := bsoncore.ReadElement(rest)
		if !ok {
			return ErrValue
		}
		key, keyErr := element.KeyBytesErr()
		if keyErr != nil || !utf8.Valid(key) || (array && !bytes.Equal(key, []byte(strconv.Itoa(index)))) {
			return ErrValue
		}
		index++
		value, err := element.ValueErr()
		if err != nil {
			return ErrValue
		}
		if err := guardRawValue(value, depth+1, limit); err != nil {
			return err
		}
		rest = next
	}
	return nil
}

func guardRawValue(value bsoncore.Value, depth, limit int) error {
	if len(value.Data) > limit {
		return ErrLimit
	}
	_, remainder, valid := bsoncore.ReadValue(value.Data, value.Type)
	if depth > maxFilterDepth || !valid || len(remainder) != 0 {
		return ErrValue
	}
	switch value.Type {
	case bsoncore.TypeEmbeddedDocument:
		return guardRawDocument(value.Data, false, depth, limit)
	case bsoncore.TypeArray:
		return guardRawDocument(value.Data, true, depth, limit)
	case bsoncore.TypeCodeWithScope:
		size, code, ok := bsoncore.ReadLength(value.Data)
		if !ok || int64(size) != int64(len(value.Data)) {
			return ErrValue
		}
		scope, err := guardRawString(code)
		if err != nil {
			return err
		}
		return guardRawDocument(scope, false, depth+1, limit)
	case bsoncore.TypeString, bsoncore.TypeJavaScript, bsoncore.TypeSymbol:
		rest, err := guardRawString(value.Data)
		if err != nil || len(rest) != 0 {
			return ErrValue
		}
	case bsoncore.TypeDBPointer:
		rest, err := guardRawString(value.Data)
		if err != nil || len(rest) != 12 {
			return ErrValue
		}
	case bsoncore.TypeBoolean:
		if value.Data[0] > 1 {
			return ErrValue
		}
	case bsoncore.TypeBinary:
		size, payload, ok := bsoncore.ReadLength(value.Data)
		if !ok || int64(size)+1 != int64(len(payload)) {
			return ErrValue
		}
		if payload[0] == 2 {
			inner, data, ok := bsoncore.ReadLength(payload[1:])
			if !ok || int64(inner) != int64(len(data)) {
				return ErrValue
			}
		}
	case bsoncore.TypeRegex:
		pattern, rest, ok := bsoncore.ReadKeyBytes(value.Data)
		if !ok || !utf8.Valid(pattern) {
			return ErrValue
		}
		options, rest, ok := bsoncore.ReadKeyBytes(rest)
		if !ok || len(rest) != 0 || !utf8.Valid(options) {
			return ErrValue
		}
		// BSON regex options must be alphabetically ordered. The driver sorts
		// on write; accepting another order would silently normalize raw BSON.
		for i := 1; i < len(options); i++ {
			if options[i] <= options[i-1] {
				return ErrValue
			}
		}
	}
	return nil
}

// guardRawString validates the length, final NUL and UTF-8 before any decoder
// copies a BSON string. The remainder permits DBPointer and code-with-scope.
func guardRawString(raw []byte) ([]byte, error) {
	size, rest, ok := bsoncore.ReadLength(raw)
	if !ok || size < 1 || int64(size) > int64(len(rest)) || rest[size-1] != 0 || !utf8.Valid(rest[:size-1]) {
		return nil, ErrValue
	}
	return rest[size:], nil
}
