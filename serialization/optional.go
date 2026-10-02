// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package serialization provides Arc JSON naming, null omission and presence-aware
// binding. It does not perform HTTP body limiting or application validation.
package serialization

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// Optional distinguishes missing, explicit null and a supplied value (even zero).
// The zero value is missing. Use Marshal to omit missing struct properties;
// encoding/json alone writes missing values as null. Values are borrowed, not cloned.
type Optional[T any] struct {
	value   T
	present bool
	null    bool
}

// Some represents a supplied value. A nil value still marshals as JSON null.
func Some[T any](value T) Optional[T] { return Optional[T]{value: value, present: true} }

// Null represents an explicit JSON null.
func Null[T any]() Optional[T] { return Optional[T]{present: true, null: true} }

// IsPresent reports whether the property was supplied, including null.
func (o Optional[T]) IsPresent() bool { return o.present }

// IsNull reports whether the property was explicitly null.
func (o Optional[T]) IsNull() bool { return o.null }

// Value returns the supplied non-null value; false means missing or explicit null.
func (o Optional[T]) Value() (T, bool) { return o.value, o.present && !o.null }

// IsZero reports missing presence, for encoding/json's omitzero support.
func (o Optional[T]) IsZero() bool { return !o.present }

// MarshalJSON emits null for missing/null, otherwise the Arc-encoded value.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.present || o.null {
		return []byte("null"), nil
	}
	return Marshal(o.value)
}

// UnmarshalJSON records explicit null, leaving o unchanged if binding fails.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	var value Optional[T]
	if err := Unmarshal(data, &value); err != nil {
		return err
	}
	*o = value
	return nil
}

// Private hooks keep presence semantics exclusive to Optional and preserve the
// codec's nesting budget instead of restarting it for every optional value.
func (o Optional[T]) optionalValue() (any, bool, bool) { return o.value, o.present, o.null }

func (o *Optional[T]) bindOptional(data []byte, depth int) error {
	if bytes.Equal(data, []byte("null")) {
		*o = Null[T]()
		return nil
	}
	var value T
	if err := unmarshal(data, reflect.ValueOf(&value).Elem(), depth+1); err != nil {
		return err
	}
	*o = Some(value)
	return nil
}

var _ json.Marshaler = Optional[int]{}
