// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package domain

// These declarations intentionally panic. The analyzer must classify their
// actual signatures without executing application markers, codecs or handlers.
type Name string

func (Name) ConceptValue() string         { panic("marker executed") }
func (Name) MarshalJSON() ([]byte, error) { panic("codec executed") }
func (*Name) UnmarshalJSON([]byte) error  { panic("codec executed") }
func (Name) MarshalText() ([]byte, error) { panic("codec executed") }
func (*Name) UnmarshalText([]byte) error  { panic("codec executed") }

type Count int

func (Count) ConceptValue() int            { panic("marker executed") }
func (Count) MarshalJSON() ([]byte, error) { panic("codec executed") }
func (*Count) UnmarshalJSON([]byte) error  { panic("codec executed") }
func (Count) MarshalText() ([]byte, error) { panic("codec executed") }
func (*Count) UnmarshalText([]byte) error  { panic("codec executed") }

type Enabled bool

func (Enabled) ConceptValue() bool           { panic("marker executed") }
func (Enabled) MarshalJSON() ([]byte, error) { panic("codec executed") }
func (*Enabled) UnmarshalJSON([]byte) error  { panic("codec executed") }
func (Enabled) MarshalText() ([]byte, error) { panic("codec executed") }
func (*Enabled) UnmarshalText([]byte) error  { panic("codec executed") }

type Ratio float64

func (Ratio) ConceptValue() float64        { panic("marker executed") }
func (Ratio) MarshalJSON() ([]byte, error) { panic("codec executed") }
func (*Ratio) UnmarshalJSON([]byte) error  { panic("codec executed") }
func (Ratio) MarshalText() ([]byte, error) { panic("codec executed") }
func (*Ratio) UnmarshalText([]byte) error  { panic("codec executed") }
