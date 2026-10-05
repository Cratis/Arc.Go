// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package declarations

import (
	cmd "github.com/cratis/arc.go/commands"
	obs "github.com/cratis/arc.go/observable"
	qry "github.com/cratis/arc.go/queries"
	val "github.com/cratis/arc.go/validation"
)

//arc:readmodel
type Model struct{ Name string }
type ModelAlias = Model

type Wrong struct{ Name string }

//arc:query model=Model
func BadQuery() (Wrong, error) { return Wrong{}, nil } // want ARC0001 BadQuery

//arc:query model=ModelAlias
func AliasBadQuery() (Wrong, error) { return Wrong{}, nil } // want ARC0001 AliasBadQuery

func (Model) GoodQuery() (qry.Page[ModelAlias], error)        { return qry.Page[ModelAlias]{}, nil }
func (Model) GoodObservable() (obs.Source[ModelAlias], error) { return nil, nil }
func (Model) UnrelatedHelper() (Wrong, error)                 { return Wrong{}, nil }

//arc:query model=Model
func GenericQuery[T any]() (Model, error) { return Model{}, nil } // want ARC0014 GenericQuery

//arc:query model=ModelAlias
func AliasGenericQuery[T any]() ([]ModelAlias, error) { return nil, nil } // want ARC0014 AliasGenericQuery
func GenericHelper[T any]() (Model, error)            { return Model{}, nil }

//arc:readmodel
type GenericModel[T any] struct{ Value T }

func (GenericModel[T]) All() (GenericModel[T], error) { return GenericModel[T]{}, nil } // want ARC0014 All

type MissingMarker struct{ Value string } // want ARC0002 MissingMarker
func (MissingMarker) Handle() error       { return nil }

type StringAlias = string
type AliasMissingMarker struct{ Value StringAlias } // want ARC0002 AliasMissingMarker
func (AliasMissingMarker) Handle() error            { return nil }

//arc:ignore
type NotACommand struct{ Value string }

func (NotACommand) Handle() error { return nil }

type EmptyHelper struct{}

func (EmptyHelper) Handle() error { return nil }

type LowercaseHelper struct{ Value string }

func (LowercaseHelper) handle() error { return nil }

//arc:command
type Command struct{ Name string }
type CommandAlias = Command

func (Command) Handle() error { return nil }

type ExternalHandler struct{}

func (ExternalHandler) Handle(CommandAlias) error { return nil } // want ARC0003 Handle

type SimilarCommand struct{}
type LookalikeHandler struct{}

func (LookalikeHandler) Handle(SimilarCommand) error { return nil }

//arc:command
type MissingHandle struct{}         // want ARC0004 MissingHandle
func (MissingHandle) handle() error { return nil }

//arc:command
type PromotedHandle struct{ CommandAlias } // want ARC0004 PromotedHandle

//arc:command
type Prepared struct{}

func (Prepared) Handle() error { return nil }
func (Prepared) Provide() (cmd.Preparation[StringAlias], error) { // want ARC0005 Provide
	return cmd.Preparation[StringAlias]{}, nil
}

//arc:command
type PlainPrepared struct{}

func (PlainPrepared) Handle() error            { return nil }
func (PlainPrepared) Provide() (string, error) { return "", nil } // want ARC0005 Provide

//arc:command
type Consumed struct{}

func (Consumed) Handle(StringAlias) error                  { return nil }
func (Consumed) Provide() (cmd.Preparation[string], error) { return cmd.Preparation[string]{}, nil }

//arc:command
type Controlled struct{}

func (Controlled) Handle() error                  { return nil }
func (Controlled) Provide() ([]val.Result, error) { return nil, nil }

// Same name is not the framework control type.
type Result struct{}

//arc:command
type LookalikeControl struct{}

func (LookalikeControl) Handle() error            { return nil }
func (LookalikeControl) Provide() (Result, error) { return Result{}, nil } // want ARC0005 Provide

//arc:command
type Injected struct{}

func (Injected) Handle(model Model) error                     { return nil }               // want ARC0006 model
func (Injected) Provide(alias ModelAlias) (val.Result, error) { return val.Result{}, nil } // want ARC0006 alias

//arc:validator
func NewValidator(model ModelAlias) *Validator { return &Validator{} } // want ARC0006 model
type Validator struct{}

//arc:command
type Optional struct{}

func (Optional) Handle(*ModelAlias, Wrong) error { return nil }
func NewOrdinaryService(Model) *Validator        { return &Validator{} }

//arc:command
//arc:authorize roles=Writer
//arc:allow-anonymous // want ARC0019 arc:allow-anonymous
type Conflicted struct{}

func (Conflicted) Handle() error { return nil }

//arc:query model=ModelAlias
//arc:allow-anonymous // want ARC0019 arc:allow-anonymous
//arc:authorize
func ConflictedQuery() (Model, error) { return Model{}, nil }

//arc:query model=Model
//arc:allow-anonymous
func AnonymousQuery() (ModelAlias, error) { return Model{}, nil }

// A query override is not a same-declaration conflict.
//
//arc:readmodel
//arc:authorize
type Protected struct{}

//arc:allow-anonymous
func (Protected) Anonymous() (Protected, error) { return Protected{}, nil }

// Lookalike comments are not Arc directives.
// arc:allow-anonymous-ish
// arc:authorize-ish
//
//arc:query model=Model
func LookalikeAuthorization() (Model, error) { return Model{}, nil }
