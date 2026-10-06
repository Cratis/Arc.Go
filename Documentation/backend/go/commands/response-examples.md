---
title: Command return shapes
description: Choose ordinary payloads, explicit responses, effects, ambiguous values, or infrastructure controls.
---

Make the client response distinct from server-consumed values. Go does not need CLR tuple or union reflection to retain Arc's one-response rule.

## Select the return grammar

| Return | Meaning |
| --- | --- |
| Ordinary nonnil O | Client response if no consumer handles it |
| NoResponse or nil | Successful void output |
| Singular validation.Result | Built-in validation control; findings survive after Handle |
| authorization.Decision | Built-in authorization control |
| Respond[R](response, effects...) | One reserved response; every effect must be consumed |
| Effects[R](effects...) | Every nonnil leaf must be consumed |
| Values[R](values...) | At most one unhandled response; ambiguity fails before effects |
| Control[R](fragment) | Explicitly merge infrastructure verdicts and diagnostics |
| Zero Outcome[R] | Successful void output |

Slices, arrays, and maps are single leaves, never implicitly flattened. Nested explicit Outcome graphs flatten in declaration order with a 64-level limit. Typed nil leaves are absent; zero, false, and empty string are present.

## Return a receipt and an event value

Declaration excerpt, importing `context` and `commands`:

```go
type ItemAdded struct {
    Name string `json:"name"`
}

type AddNamedItem struct {
    Name string `json:"name"`
}

func (c AddNamedItem) Handle(context.Context) (commands.Outcome[string], error) {
    return commands.Respond("receipt", ItemAdded(c)), nil
}
```

Register a matching ItemAdded consumer before executing this command. Otherwise the required effect fails with `ErrUnhandledEffect`. The compiled `ExampleRespond` includes that consumer and prints `receipt Book`; it records an in-memory value rather than appending to Chronicle.

## Avoid false controls

A bare `[]validation.Result` from Handle is a payload, not a validation result list. A bare `commands.Result` is also an ordinary DTO. Use `Control` to explicitly adopt an infrastructure result. A user-defined type named Result or Union has no special behavior.

`WithResponseType[C,R]` enforces response assignability at runtime, and `WithNoResponse[C]` rejects unconsumed responses. `Registration.ReturnType` is raw O; it need not be the client response type. Conditional consumers leave response metadata unknown unless a contract is supplied.

## Run the examples

From the module root:

```bash
go test ./commands -run Example
```

The external-package examples cover result method expressions, type-only void registration, dependencies through a closure, model Validate, typed Provide, scoped resources, and returned events-as-values. Examples execute real pipelines without a container. HTTP delivery, proxies, operations, and persistence are outside this demonstration.
