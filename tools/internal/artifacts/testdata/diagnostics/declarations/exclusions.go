// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package declarations

//arc:model
type WireModel struct{ Name string }

func (WireModel) Handle() error { return nil }

//arc:derived interface=any
type DerivedModel struct{ Name string }

func (DerivedModel) Handle() error { return nil }

//arc:policy name=Policy
type Policy struct{ Name string }

func (Policy) Handle() error { return nil }

//arc:validator
type SelectedValidator struct{ Name string }

func (SelectedValidator) Handle() error { return nil }

//arc:readmodel
type OtherReadModel struct{ Name string }

func (OtherReadModel) Handle() error { return nil }

//arc:enum
type Choice int32

func (Choice) Handle() error { return nil }

type UtilityHelper struct{ Name string }

func (UtilityHelper) Handle() error { return nil }

type UtilityHelpers struct{ Name string }

func (UtilityHelpers) Handle() error { return nil }

type UtilityExtensions struct{ Name string }

func (UtilityExtensions) Handle() error { return nil }

// A same-named generic function is not commands.Register.
func Register[T any]() {}

type LookalikeRegistered struct{ Value string } // want ARC0002 LookalikeRegistered

func (LookalikeRegistered) Handle() error { return nil }

func RegisterLookalike() { Register[LookalikeRegistered]() }
