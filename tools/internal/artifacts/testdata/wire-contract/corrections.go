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
