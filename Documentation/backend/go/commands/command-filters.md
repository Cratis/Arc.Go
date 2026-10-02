---
title: Command filters
description: Add ordered command verdicts without replacing mandatory authorization or validation.
---

Use a filter for a reusable execution gate, not to hide command business logic in a second handler. `Filter.OnExecution(context.Context, *Invocation)` returns a `Result[NoResponse]` fragment and an inspectable local error. `FilterFunc` adapts a callback.

## Register lazily

`AddFilter(name, factory, keys...)` registers an ordinary filter. `AddAuthorizationFilter` registers the authorization category; `AsAuthorizationFilter` wraps an existing Filter. Names identify extensions, not DI keys. Duplicate names within the filter category fail deterministically.

Factories run only at their stage and return borrowed instances; resource holders own disposal. A direct instance captured by a factory must support concurrent independent roots. Build checks declared keys without resolving them.

## Preserve phase order

Membership and declaration authorization always run first. Custom authorization filters follow, then ordinary filters in registration order, then model and explicit validation. Registration order cannot move an ordinary validator ahead of authorization.

Fragments AND authorization and append diagnostics, preserving the first denial reason and outer correlation. Input-stage severity filtering discards nonblocking findings before deciding continuation. A zero fragment denies; continuation requires explicit `Success`. There is no C#-style null-result skip.

## Keep the gate safe

Panics and callback errors pass through application boundaries; cancellation never becomes validation. Exception-bearing fragments are production-redacted. Validation findings and explicit denial messages are caller-authored feedback: do not place secrets in them.

A filter runs during both Execute and Validate. It must not use Handle-like external writes as a substitute for preparation or effect handling. Validate-only bound executors refuse Execute, and ignored nested failures remain sticky.
