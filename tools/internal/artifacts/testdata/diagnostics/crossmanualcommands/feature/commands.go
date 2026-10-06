// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package feature

import (
	"context"

	"github.com/cratis/arc.go/commands"
)

//arc:readmodel
type Model struct{ Name string }

// Greet is manually registered by an importing package.
type Greet struct{ Name string }

func (c Greet) Handle(context.Context) (string, error) { return c.Name, nil }

// IgnoredGreet uses the documented suppression for cross-package registration.
//
//arc:ignore
type IgnoredGreet struct{ Name string }

func (c IgnoredGreet) Handle(context.Context) (string, error) { return c.Name, nil }

// Wrapped is manually registered through a generic wrapper in this package.
type Wrapped struct{ Name string }

func (c Wrapped) Handle(context.Context) (string, error) { return c.Name, nil }

//arc:ignore
type IgnoredWrapped struct{ Name string }

func (c IgnoredWrapped) Handle(context.Context) (string, error) { return c.Name, nil }

func register[T any](registrar commands.Registrar, handler commands.Handler[T, string]) error {
	return commands.Register[T](registrar, handler)
}

func RegisterWrapped(registrar commands.Registrar) error {
	return register(registrar, commands.Handle(Wrapped.Handle))
}

func RegisterIgnoredWrapped(registrar commands.Registrar) error {
	return register(registrar, commands.Handle(IgnoredWrapped.Handle))
}
