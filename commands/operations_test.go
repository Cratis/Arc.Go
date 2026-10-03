// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

type declarationOperation struct{ Name string }

func (declarationOperation) CommandOperation()                       {}
func (declarationOperation) Execute(context.Context, struct{}) error { return nil }
func (declarationOperation) Compensate(context.Context, struct{}, commands.OperationFailure) error {
	return nil
}

type malformedCompensator struct{}

func (malformedCompensator) CommandOperation()                       {}
func (malformedCompensator) Execute(context.Context, struct{}) error { return nil }
func (malformedCompensator) Compensate(context.Context) error        { panic("must not invoke") }

type pointerCompensator struct{}

func (pointerCompensator) CommandOperation()                       {}
func (pointerCompensator) Execute(context.Context, struct{}) error { return nil }
func (*pointerCompensator) Compensate(context.Context, struct{}, commands.OperationFailure) error {
	return nil
}

type operationDeclarationCommand struct{}

func (operationDeclarationCommand) Handle(context.Context) (commands.Operations, error) {
	return commands.Operations{}, nil
}

func declarationDependencies(context.Context, *execution.Scope) (struct{}, error) {
	return struct{}{}, nil
}

func TestOperationsCopyMembershipAndBorrowPayloads(t *testing.T) {
	pointer := &declarationOperation{Name: "borrowed"}
	input := []commands.Operation{declarationOperation{Name: "first"}, pointer}
	batch, err := commands.NewOperations(input...)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = declarationOperation{Name: "changed"}
	values := batch.Values()
	values[0] = declarationOperation{Name: "also changed"}
	if got := batch.Values()[0].(declarationOperation).Name; got != "first" {
		t.Fatalf("membership mutated: %s", got)
	}
	if got := batch.Values()[1]; got != pointer {
		t.Fatal("payload not borrowed")
	}
	if len((commands.Operations{}).Values()) != 0 {
		t.Fatal("zero batch not empty")
	}
	for _, value := range []commands.Operation{nil, (*declarationOperation)(nil)} {
		if _, err := commands.NewOperations(value); !errors.Is(err, commands.ErrInvalidOperation) {
			t.Fatalf("nil member = %v", err)
		}
	}
}

func TestOperationRegistrationValidatesWithoutActivation(t *testing.T) {
	var registry commands.Registry
	calls := 0
	factory := func(context.Context, *execution.Scope) (struct{}, error) { calls++; return struct{}{}, nil }
	if err := commands.RegisterOperation[declarationOperation](&registry, factory); err != nil {
		t.Fatal(err)
	}
	if err := commands.RegisterOperation[declarationOperation](&registry, factory); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
	if err := commands.RegisterOperation[malformedCompensator](&registry, factory); !errors.Is(err, commands.ErrInvalidOperation) {
		t.Fatal(err)
	}
	if err := commands.RegisterOperation[pointerCompensator](&registry, factory); !errors.Is(err, commands.ErrInvalidOperation) {
		t.Fatal(err)
	}
	if err := commands.RegisterOperation[*pointerCompensator](&registry, factory); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("registration activated dependencies")
	}
	if _, err := registry.Build(commands.PipelineOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("build activated dependencies")
	}
	if err := commands.RegisterOperation[malformedCompensator](&registry, factory); !errors.Is(err, commands.ErrFrozen) {
		t.Fatal(err)
	}
}

func TestOperationDeclarationFailsClosed(t *testing.T) {
	var registry commands.Registry
	if err := commands.Register[operationDeclarationCommand](&registry, commands.Handle(operationDeclarationCommand.Handle)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Build(commands.PipelineOptions{}); !errors.Is(err, commands.ErrInvalidOperation) {
		t.Fatalf("unimplemented boundary = %v", err)
	}
	entry, err := commands.NewRegistry(commands.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[operationDeclarationCommand](entry, commands.Handle(func(operationDeclarationCommand, context.Context) ([]commands.Operation, error) { return nil, nil })); !errors.Is(err, commands.ErrInvalidOperation) {
		t.Fatal(err)
	}
	if err := commands.Register[operationDeclarationCommand](entry, commands.Handle(func(operationDeclarationCommand, context.Context) (any, error) { return nil, nil }), commands.WithOperations[operationDeclarationCommand]()); !errors.Is(err, commands.ErrInvalidOperation) {
		t.Fatal(err)
	}
	if err := commands.Register[operationDeclarationCommand](entry, commands.Handle(operationDeclarationCommand.Handle), commands.WithResponseType[operationDeclarationCommand, commands.Operations]()); !errors.Is(err, commands.ErrInvalidOperation) {
		t.Fatal(err)
	}
}

func TestOperationDependencyCatalogCheck(t *testing.T) {
	var registry commands.Registry
	key := di.KeyFor[string]()
	if key.Type() != reflect.TypeFor[string]() {
		t.Fatal("key type")
	}
	if err := commands.RegisterOperation[declarationOperation](&registry, declarationDependencies, key); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Build(commands.PipelineOptions{}); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatalf("missing static key = %v", err)
	}
}

func TestErasedOperationCannotEscapeAsResponse(t *testing.T) {
	var registry commands.Registry
	if err := commands.Register[operationDeclarationCommand](&registry, commands.Handle(func(operationDeclarationCommand, context.Context) (any, error) { return declarationOperation{}, nil })); err != nil {
		t.Fatal(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.Execute(context.Background(), operationDeclarationCommand{})
	if !errors.Is(err, commands.ErrInvalidOperation) || result.IsSuccess() {
		t.Fatalf("erased operation = %v, %v", result, err)
	}
	if _, present := result.Response(); present {
		t.Fatal("operation descriptor escaped")
	}
}
