// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package concepts provides Arc's portable UUID and calendar scalar values.
// Values are immutable by convention and safe to copy. They are not Chronicle
// event source IDs: those may be arbitrary strings.
package concepts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// UUID contains 16 bytes in RFC network order, not .NET Guid.ToByteArray order.
// Its zero value is Guid.Empty. JSON and text use lowercase dashed notation.
// A new named type based on UUID must explicitly forward its codec methods.
type UUID [16]byte

// ParseUUID accepts the dashed Guid D format, including uppercase hex digits.
func ParseUUID(text string) (UUID, error) {
	var id UUID
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
		return id, fmt.Errorf("UUID requires dashed 8-4-4-4-12 notation")
	}
	compact := strings.ReplaceAll(text, "-", "")
	if len(compact) != 32 {
		return id, fmt.Errorf("UUID contains misplaced separators")
	}
	_, err := hex.Decode(id[:], []byte(compact))
	if err != nil {
		return UUID{}, fmt.Errorf("invalid UUID: %w", err)
	}
	return id, nil
}

// NewUUID returns a cryptographically random version 4 UUID.
func NewUUID() (UUID, error) {
	var id UUID
	if _, err := rand.Read(id[:]); err != nil {
		return UUID{}, fmt.Errorf("generate UUID: %w", err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return id, nil
}

// String returns canonical dashed text.
func (id UUID) String() string {
	text := hex.EncodeToString(id[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

// IsZero reports whether id is Guid.Empty.
func (id UUID) IsZero() bool { return id == UUID{} }

// MarshalText implements encoding.TextMarshaler.
func (id UUID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

// UnmarshalText parses dashed text without changing id on failure.
func (id *UUID) UnmarshalText(text []byte) error {
	value, err := ParseUUID(string(text))
	if err != nil {
		return err
	}
	*id = value
	return nil
}

// MarshalJSON emits a JSON string.
func (id UUID) MarshalJSON() ([]byte, error) { return json.Marshal(id.String()) }

// UnmarshalJSON rejects null; use a pointer or serialization.Optional for nullability.
func (id *UUID) UnmarshalJSON(data []byte) error { return unmarshalString(data, id.UnmarshalText) }

func unmarshalString(data []byte, parse func([]byte) error) error {
	var text *string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	if text == nil {
		return fmt.Errorf("scalar must be a string, not null")
	}
	return parse([]byte(*text))
}
