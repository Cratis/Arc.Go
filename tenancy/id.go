// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidID identifies invalid tenant text or non-string JSON without exposing input.
var ErrInvalidID = errors.New("invalid tenant ID")

// ID is a comparable immutable tenant identifier. Zero means NotSet.
type ID struct{ text string }

// ParseID preserves case and interior spaces; empty and [NotSet] yield zero.
// Control characters, invalid UTF-8, and surrounding whitespace are rejected.
func ParseID(text string) (ID, error) {
	if !utf8.ValidString(text) || strings.TrimSpace(text) != text {
		return ID{}, ErrInvalidID
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return ID{}, ErrInvalidID
		}
	}
	if text == "" || text == "[NotSet]" {
		return ID{}, nil
	}
	return ID{text: text}, nil
}

// Default returns the named Default tenant, distinct from zero.
func Default() ID { return ID{text: "Default"} }

// String emits [NotSet] for zero, otherwise the original text.
func (id ID) String() string {
	if !id.IsSet() {
		return "[NotSet]"
	}
	return id.text
}

// IsSet reports whether the tenant is distinct from NotSet.
func (id ID) IsSet() bool { return id.text != "" }

// IsDefault includes both NotSet and the named Default, without equating them.
func (id ID) IsDefault() bool { return !id.IsSet() || id == Default() }

// MarshalText emits sentinel-aware tenant text.
func (id ID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

// UnmarshalText changes the receiver only after successful parsing.
func (id *ID) UnmarshalText(text []byte) error {
	value, err := ParseID(string(text))
	if err != nil {
		return err
	}
	if id == nil {
		return ErrInvalidID
	}
	*id = value
	return nil
}

// MarshalJSON emits a JSON string.
func (id ID) MarshalJSON() ([]byte, error) { return json.Marshal(id.String()) }

// UnmarshalJSON accepts only strings and leaves the receiver unchanged on failure.
func (id *ID) UnmarshalJSON(data []byte) error {
	var text *string
	if err := json.Unmarshal(data, &text); err != nil || text == nil {
		return ErrInvalidID
	}
	return id.UnmarshalText([]byte(*text))
}
