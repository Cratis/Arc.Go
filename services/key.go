// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import (
	"reflect"
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
		return "[" + strings.TrimSuffix(strings.SplitN(t.String(), "]", 2)[0], "[") + "]" + typeIdentity(t.Elem())
	case reflect.Map:
		return "map[" + typeIdentity(t.Key()) + "]" + typeIdentity(t.Elem())
	default:
		return t.String()
	}
}
