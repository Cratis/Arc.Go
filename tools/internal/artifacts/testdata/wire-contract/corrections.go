// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

//arc:namespace Shop
package consumer

import (
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/serialization"
)

//arc:command
type Save struct {
	Span  concepts.TimeSpan
	Date  concepts.DateOnly
	Clock concepts.TimeOnly
	Value Presence
}

func (Save) Handle() error { return nil }

type Presence struct {
	Pointer  *int
	Optional *serialization.Optional[int]
	Inner    **int
	Nested   **serialization.Optional[int]
}

type State uint64
type SignedState int16
type NullableState = *State

type NullableCodec struct{}

func (NullableCodec) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
func (*NullableCodec) UnmarshalJSON([]byte) error  { return nil }

// NilableCodecs separates immediate nil omission from a codec-produced null.
type NilableCodecs struct {
	Value      NullableSlice
	Map        NullableMap
	Interface  NullableCodecInterface
	Slice      []int
	Dictionary map[string]int
	NamedSlice PlainSlice
	NamedMap   PlainMap
}

type NullableSlice []int

func (NullableSlice) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
func (*NullableSlice) UnmarshalJSON([]byte) error  { return nil }

type NullableMap map[string]int

func (NullableMap) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
func (*NullableMap) UnmarshalJSON([]byte) error  { return nil }

type NullableCodecInterface interface {
	MarshalJSON() ([]byte, error)
}

type PlainSlice []int
type PlainMap map[string]int
