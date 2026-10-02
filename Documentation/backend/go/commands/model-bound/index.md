---
title: Model-bound commands
description: Define a command's Handle, Validate, and Provide methods and register typed adapters.
---

Keep the intent and its decision together. A command is a named Go struct, and its receiver is the decoded command, not a DI-created handler object.

## Define the behavior

Declaration excerpt, with `context` imported:

```go
type Greet struct {
    Name string `json:"name"`
}

func (c Greet) Handle(context.Context) (string, error) {
    return "Hello, " + c.Name, nil
}
```

The method remains ordinary Go. Calling it directly bypasses the pipeline; use pipeline execution when you need authorization, validation, resources, or returned effects.

Composition excerpt using this declaration and the `commands` package:

```go
var registry commands.Registry
err := commands.Register(&registry, commands.Handle(Greet.Handle))
```

Check `err` before building. The adapter carries the exact receiver and raw return type; registration does not invoke the method. The executable `ExampleHandle` in `commands/example_test.go` builds a real pipeline and prints `true true Hello, Ada`.

## Use type-only registration for void methods

`commands.Register[ClearLocalCache](&registry)` needs no adapter when `ClearLocalCache` implements exactly `Handle(context.Context) error`. A value registration never synthesizes a pointer receiver. Pointer and value registrations are exact, distinct types; give them distinct public identities if you register both.

Go cannot infer an arbitrary method return type from the command type alone. Result-bearing methods therefore use `Handle(C.Handle)`. Methods with services use a closure, `Invoke`, or `Scoped`. The compiled examples cover each approach without a container.

## Separate preparation

`WithProvide(C.Provide, C.Handle)` passes one exact typed payload to Handle after authorization and input validation. Multiple prepared dependencies belong in an application struct. A supplied nil pointer remains an explicit payload; it never falls back to DI.

Use `WithPreparation` when Provide needs controls: `Provided` carries a usable payload, `ProvidedWith` adds a verdict, and `StopProviding` always skips Handle, including successful early termination. Zero `Preparation` fails with `ErrInvalidPreparation`. Provide errors never fabricate a payload. Validate invokes neither stage.

## Add model validation and metadata

A matching `Validate(context.Context) ([]validation.Result, error)` runs automatically through the validation graph. This is a Go convention, not a claim that C# invokes a command Validate method. Tags such as `validate:"required"` describe supported input rules; [validation](../command-validation.md) defines their limits.

Public identity defaults to the named type and the registry's explicitly configured namespace, or global. Never derive routes from a Go import path. Pin migrations with `WithDescriptor`; individual metadata options override it regardless of option order. Repeated singular options and multiple handlers fail.

An optional blank declaration field can carry runtime-readable metadata:

```go
type PublishItem struct {
    _ struct{} `json:"-" arc:"command,name=PublishItem,path=/items/publish" authorize:"roles=Editor|Admin"`
    ID string `json:"id" arc:"key"`
}
```

This excerpt declares metadata only; supply a supported Handle method or adapter before registering it. Declaration authorization uses the same compiler as explicit options. See [authorization](authorization.md) before exposing the command.
