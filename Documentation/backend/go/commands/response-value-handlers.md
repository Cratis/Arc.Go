---
title: Response value handlers
description: Consume returned values additively and update command context before selecting a client response.
---

A returned value can represent work for a server extension rather than a client response. Register a `ResponseValueHandler` when that interpretation belongs to an integration; keep ordinary command logic on the command.

## Register a consumer

`AddResponseValueHandler(name, factory, keys...)` registers a dynamic consumer. `RegisterResponseValueHandler[T]` additionally restricts applicability to exact concrete T or values implementing interface T; its `CanHandle` predicate still decides at runtime. Factories run only after Handle, never during Build or Validate, and return borrowed instances. Resources own disposal.

The methods are:

- `CanHandle(CommandContext, any) bool` determines applicability.
- `Handle(context.Context, *Invocation, any) (Result[NoResponse], error)` consumes a matching value and contributes a verdict.
- Optional `UpdateContext(context.Context, *Invocation, any) error` writes context values before response selection.

Every matching handler runs in registration order. This is additive dispatch, not first-match selection. Shared instances must support concurrent calls; do not capture a request scope in a long-lived shared consumer.

## Understand the passes

1. Flatten explicit outcome graphs in declaration order; typed nil leaves disappear, while scalar zeros remain.
2. Run matching context updaters before selecting a response.
3. Reserve an explicit Respond response or select at most one unhandled Values leaf.
4. Reevaluate consumer predicates with the selected response installed.
5. Preflight ambiguity, required consumption, and built-in controls before executing effects.

A predicate may depend on the selected response. With Values, declaration order matters: put the response before an effect that becomes consumable only when that response exists. Respond reserves the response explicitly.

## Preserve the control boundary

Only singular `validation.Result` and `authorization.Decision` are built-in plain controls. A returned command result DTO or validation collection remains an ordinary payload unless you explicitly adopt it with `Control` or register a consumer.

Dynamic consumers do not establish an unconditional no-response contract. Use `Outcome[R]`, `WithResponseType`, or `WithNoResponse` when tooling or typed callers need an enforceable contract. See [response examples](response-examples.md) for the return grammar.

Events-as-values require a real consumer. The compiled `ExampleRespond` records an event in memory; it is not Chronicle persistence, append, or a transaction. No event-shaped struct is automatically magical here.
