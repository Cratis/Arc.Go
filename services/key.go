// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import (
	"reflect"
	"strconv"
	"strings"
)

// Key identifies an exact Go type, including interface and pointer distinctions.
// Zero is invalid as a dependency key.
type Key struct{ typ reflect.Type }

// KeyFor returns the exact key for T, including interface types.
func KeyFor[T any]() Key { return Key{typ: reflect.TypeFor[T]()} }

// String returns a package-qualified type identity, or <invalid> for zero.
func (k Key) String() string {
	if k.typ == nil {
		return "<invalid>"
	}
	return typeIdentity(k.typ)
}

func typeIdentity(t reflect.Type) string {
	if t.Name() != "" {
		if t.PkgPath() != "" {
			return t.PkgPath() + "." + t.Name()
		}
		return t.Name()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + typeIdentity(t.Elem())
	case reflect.Slice:
		return "[]" + typeIdentity(t.Elem())
	case reflect.Array:
		return "[" + strconv.Itoa(t.Len()) + "]" + typeIdentity(t.Elem())
	case reflect.Map:
		return "map[" + typeIdentity(t.Key()) + "]" + typeIdentity(t.Elem())
	case reflect.Chan:
		return t.ChanDir().String() + " " + typeIdentity(t.Elem())
	case reflect.Func:
		return functionIdentity(t)
	case reflect.Struct:
		fields := make([]string, t.NumField())
		for i := range fields {
			f := t.Field(i)
			fields[i] = f.PkgPath + ":" + f.Name + " " + typeIdentity(f.Type) + " " + strconv.Quote(string(f.Tag)) + " " + strconv.FormatBool(f.Anonymous)
		}
		return "struct{" + strings.Join(fields, ";") + "}"
	case reflect.Interface:
		methods := make([]string, t.NumMethod())
		for i := range methods {
			m := t.Method(i)
			methods[i] = m.PkgPath + ":" + m.Name + typeIdentity(m.Type)
		}
		return "interface{" + strings.Join(methods, ";") + "}"
	default:
		return t.String()
	}
}
func functionIdentity(t reflect.Type) string {
	inputs := make([]string, t.NumIn())
	outputs := make([]string, t.NumOut())
	for i := range inputs {
		inputs[i] = typeIdentity(t.In(i))
		if t.IsVariadic() && i == len(inputs)-1 {
			inputs[i] = "..." + typeIdentity(t.In(i).Elem())
		}
	}
	for i := range outputs {
		outputs[i] = typeIdentity(t.Out(i))
	}
	return "func(" + strings.Join(inputs, ",") + ")(" + strings.Join(outputs, ",") + ")"
}
