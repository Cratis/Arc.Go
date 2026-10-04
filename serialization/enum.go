// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidEnumValue identifies an input rejected by Int32Enum.ParseJSON.
// Errors do not include the supplied payload.
var ErrInvalidEnumValue = errors.New("invalid Int32 enum JSON value")

// Int32Enum parses the explicit Int32 enum profile used by Arc 22.48.2 with
// Fundamentals 7.19.6. Construct it with NewInt32Enum. It is immutable and safe
// for concurrent use; a nil receiver or zero value rejects all input.
//
// Numeric JSON accepts only declared values. Case-insensitive original names,
// comma-separated names (bitwise OR, even without Flags), and decimal strings
// are accepted. Decimal strings may contain ANY Int32, including undeclared
// values. Ordinary numeric JSON writes likewise retain the full Int32 domain:
// this profile deliberately does not promise that every write can be read back.
// Later Fundamentals versions have different numeric flags admission.
//
// Use ParseJSON from a named Int32 type's UnmarshalJSON method. Its normal JSON
// encoding already writes numbers; no global registration or marshal hook is
// needed. Pointers and Optional own null/presence outside this scalar parser.
// This does not change ordinary Go integer decoding or add generator metadata.
// Other backing widths and non-ASCII parse-name declarations are unsupported.
// TypeScript export renames must not be supplied as original parse names.
type Int32Enum[T ~int32] struct {
	names  map[string]T
	values map[T]struct{}
}

// NewInt32Enum copies original parse names and their values. Names must be
// nonempty ASCII identifiers and unique ignoring ASCII case; multiple names may
// have the same value. Nil/empty declarations are invalid. Rejecting ambiguous
// case-only names is a deliberate Go admission restriction, not a CLR rule.
// The caller may subsequently change the input map without affecting the parser.
func NewInt32Enum[T ~int32](members map[string]T) (*Int32Enum[T], error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("Int32 enum requires declared parse names")
	}
	result := &Int32Enum[T]{names: make(map[string]T, len(members)), values: make(map[T]struct{}, len(members))}
	for name, value := range members {
		if !enumIdentifier(name) {
			return nil, fmt.Errorf("Int32 enum parse names must be ASCII identifiers")
		}
		name = strings.ToLower(name)
		if _, exists := result.names[name]; exists {
			return nil, fmt.Errorf("Int32 enum parse names must be unique ignoring case")
		}
		result.names[name] = value
		result.values[value] = struct{}{}
	}
	return result, nil
}

// ParseJSON reads one complete scalar JSON value without retaining input. On
// failure it returns zero and ErrInvalidEnumValue, never a partially parsed OR
// combination. Null is rejected; use a pointer/Optional for nullable values.
func (e *Int32Enum[T]) ParseJSON(data []byte) (T, error) {
	if e == nil || len(e.names) == 0 || !utf8.Valid(data) || !json.Valid(data) {
		return 0, ErrInvalidEnumValue
	}
	data = bytes.TrimSpace(data)
	if data[0] != '"' {
		var value int32
		if bytes.Equal(data, []byte("null")) || json.Unmarshal(data, &value) != nil {
			return 0, ErrInvalidEnumValue
		}
		if _, defined := e.values[T(value)]; !defined {
			return 0, ErrInvalidEnumValue
		}
		return T(value), nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return 0, ErrInvalidEnumValue
	}
	// Enum.TryParse trims Unicode leading whitespace before choosing its
	// numeric or name path. The numeric path's trailing whitespace is ASCII.
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	if text == "" {
		return 0, ErrInvalidEnumValue
	}
	if text[0] == '+' || text[0] == '-' || text[0] >= '0' && text[0] <= '9' {
		// .NET integer parsing accepts trailing NULs, but not embedded NULs
		// or whitespace after a NUL. Preserve this pinned parser behavior.
		number := strings.TrimRight(strings.TrimRight(text, "\x00"), " \t\r\n\v\f")
		value, err := strconv.ParseInt(number, 10, 32)
		if err != nil {
			return 0, ErrInvalidEnumValue
		}
		return T(value), nil
	}
	var result T
	for name := range strings.SplitSeq(text, ",") {
		name = strings.TrimSpace(name)
		if !enumIdentifier(name) {
			return 0, ErrInvalidEnumValue
		}
		value, found := e.names[strings.ToLower(name)]
		if !found {
			return 0, ErrInvalidEnumValue
		}
		result |= value
	}
	return result, nil
}

func enumIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for index := range len(name) {
		character := name[index]
		switch {
		case character == '_', character >= 'A' && character <= 'Z', character >= 'a' && character <= 'z':
		case index > 0 && character >= '0' && character <= '9':
		default:
			return false
		}
	}
	return true
}
