//go:build arcdiagnostics

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package declarations

//arc:query model=Model
func TaggedWrong() (Wrong, error) { return Wrong{}, nil } // want ARC0001 TaggedWrong

type TaggedMissingMarker struct{ Name string } // want ARC0002 TaggedMissingMarker
func (TaggedMissingMarker) Handle() error      { return nil }

type TaggedExternal struct{}

func (TaggedExternal) Handle(CommandAlias) error { return nil } // want ARC0003 Handle

//arc:command
type TaggedMissingHandle struct{} // want ARC0004 TaggedMissingHandle

//arc:command
type TaggedPreparation struct{}

func (TaggedPreparation) Handle() error            { return nil }
func (TaggedPreparation) Provide() (string, error) { return "", nil } // want ARC0005 Provide

//arc:command
type TaggedInjected struct{}

func (TaggedInjected) Handle(model ModelAlias) error { return nil } // want ARC0006 model

//arc:query model=Model
func TaggedGeneric[T any]() (Model, error) { return Model{}, nil } // want ARC0014 TaggedGeneric

//arc:query model=Model
//arc:authorize policy=Write
//arc:allow-anonymous // want ARC0019 arc:allow-anonymous
func TaggedConflict() (Model, error) { return Model{}, nil }
