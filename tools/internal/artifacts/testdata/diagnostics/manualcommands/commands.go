// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package manualcommands

import (
	"context"

	cmd "github.com/cratis/arc.go/commands"
)

// A selected artifact enables the heuristic in this mixed-registration package.
//
//arc:model
type Marker struct{ Name string }

// This is the supported getting-started command shape, without a directive.
type Greet struct{ Name string }

func (c Greet) Handle(_ context.Context) (string, error) { return "Hello, " + c.Name, nil }

type GreetAlias = Greet

func Register(registrar cmd.Registrar) error {
	return cmd.Register[GreetAlias](registrar, cmd.Handle(GreetAlias.Handle))
}

type PointerCommand struct{ Value string }

func (*PointerCommand) Handle(context.Context) error { return nil }

func RegisterPointer(registrar cmd.Registrar) error {
	return cmd.Register[*PointerCommand](registrar)
}

type InferredCommand struct{ Value string }

func (InferredCommand) Handle(context.Context) (string, error) { return "", nil }

func RegisterInferred(registrar cmd.Registrar) error {
	return cmd.Register(registrar, cmd.Handle(InferredCommand.Handle))
}

type ResponseConsumer struct{ Name string }

func (*ResponseConsumer) CanHandle(cmd.CommandContext, any) bool { return true }
func (*ResponseConsumer) Handle(context.Context, *cmd.Invocation, any) (cmd.Result[cmd.NoResponse], error) {
	return cmd.Result[cmd.NoResponse]{}, nil
}

var _ cmd.ResponseValueHandler = (*ResponseConsumer)(nil)
