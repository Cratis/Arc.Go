// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package declarations

import "github.com/cratis/arc.go/commands"

// Matching spelling is not Arc framework identity.
type Page[T any] struct{ Items []T }
type Source[T any] interface{ Lookalike() T }

//arc:query model=Model
func FakePageQuery() (Page[Model], error) { return Page[Model]{}, nil } // want ARC0001 FakePageQuery

//arc:query model=Model
func FakeSourceQuery() (Source[Model], error) { return nil, nil } // want ARC0001 FakeSourceQuery

// Generic query-shaped methods on an unmarked type are not Arc queries.
type ReadModel[T any] struct{ Value T }

func (ReadModel[T]) All() (ReadModel[T], error) { return ReadModel[T]{}, nil }

// Alias of an actual control type stays exempt, unlike the local Result.
type ControlAlias = commands.Result[commands.NoResponse]

//arc:command
type AliasControl struct{}

func (AliasControl) Handle() error                  { return nil }
func (AliasControl) Provide() (ControlAlias, error) { return ControlAlias{}, nil }

// Neither a distinct value type nor a shape lookalike is a read-model dependency.
type ModelLookalike Model

//arc:command
type UnrelatedDependencies struct{}

func (UnrelatedDependencies) Handle(ModelLookalike, ReadModel[string]) error { return nil }

// A directive-lookalike must not opt in a type that happens to lack Handle.
//
//arc:command-ish
type CommandLookalike struct{}

//arc:ignore
type IgnoredMissingCommand struct{}

func (IgnoredMissingCommand) Handle() error { return nil }

//arc:ignore
type IgnoredExternal struct{}

func (IgnoredExternal) Handle(CommandAlias) error { return nil }
