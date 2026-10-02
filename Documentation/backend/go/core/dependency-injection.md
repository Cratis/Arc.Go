---
title: Dependency injection and operation resources
description: Wire dependencies with plain Go, then opt into Fundamentals scopes when you need a container.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

You do not need a container to use Arc. Construct your dependencies normally and
pass them through constructors, closures, or an operation resource holder. An Arc
scope adds security continuity and cleanup ownership, not automatic authorization
or a transaction.

## Start with plain Go

A holder needs only `Close(context.Context) error`. Use
`execution.RunWithResources` to open it for one operation, and
`execution.ResourcesAs[T]` inside the callback to borrow the typed holder. The
[no-container example](../../../../examples/nocontainer/main.go) combines this
with real authorization and validation calls; run it from the repository root:

```sh
go run ./examples/nocontainer
```

It prints `authorized: true`, `validation findings: 0`, `hello, Arc`, and
`resources closed: true`. It does not register a command or query pipeline; those
pipelines are not implemented yet.

## Choose operation ownership

- `OpenScope(ctx, open)` owns the opened holder. A nil opener creates an empty
  scope. A holder returned with an error is still cleaned up.
- `BorrowScope(ctx, holder)` never closes the holder. You must keep it alive until
  the wrapper's admitted work finishes. Typed-nil holders are rejected.
- `scope.Use(ctx, callback)` admits synchronous work. Its non-closing scope view
  expires when the callback returns. Do not retain it or its resolver.
- `scope.Close(ctx)` stops admission, joins admitted work, and forwards owned
  cleanup at most once, retaining its outcome for repeated calls. It never
  completes command effects. Do not close an owning scope from its own callback.

Scopes capture principal and tenant values **and presence**. Changing either
invalidates scope use; changing correlation or receipt does not. A scope is not
an authorization verdict. `Resources()` is a trusted adapter escape hatch, not a
revocable resource reference: use it only inside admitted work and never close or
retain its holder. `ResourcesAs[T]` checks type and security but does not resolve
anything.

## Opt into Fundamentals dependency injection

[Fundamentals.Go](https://github.com/Cratis/Fundamentals.Go) owns the DI contracts
and optional container. Arc owns `Resources` and `OpenResources`; a `di.Scope`
satisfies `Resources` structurally. You can adapt any `di.ScopeFactory` through
`execution.ResourcesFrom(factory)` without adopting the default container.

For the optional default container, install Arc's guard when you build it. This
excerpt assumes `registry` is a populated `container.Registry` and imports
`execution` and `github.com/cratis/fundamentals.go/dependencyinjection/container`:

```go
provider, err := registry.Build(container.WithContextGuard(execution.ContextGuard()))
```

Inspect `err`, and keep the provider alive until operations finish. The guard
also protects factory-internal scoped resolutions that do not pass through an
Arc resolver. Singleton dependency paths intentionally hide request values;
request-specific dependencies belong in scoped registrations, not singletons.

Pass `execution.ResourcesFrom(provider)` to `RunWithResources`. Inside its
callback, use `execution.Resolve[T](ctx, scope)` or
`di.Resolve[T](ctx, scope.Resolver())`. Each resolution checks security and
cancellation before and after dependency code and is admitted against concurrent
Arc cleanup. Returned dependencies are borrowed for the operation's lifetime.
Plain holders without a resolver return `execution.ErrNoResolver` on this path;
nil or unconstructed Arc scopes return `execution.ErrInvalidScope`.

Keys are exact: registering a concrete type does not register its interfaces or
pointer/value counterparts. The provider has no root resolver. Factories receive
restricted, expiring `di.Resolver` views; declare their dependency edges and use
`di.BindBorrowed` for interface forwarding so one owned concrete instance is not
disposed twice. Read the
[Fundamentals DI guide](https://github.com/Cratis/Fundamentals.Go/blob/develop/Documentation/dependency-injection.md)
for registration, lifetimes, graph checks, adapter conformance and errors.

## Borrow a compatible DI scope

When an operation is configured with a provider and borrows DI resources, call
`scope.CheckOwner(ctx, provider)` before using them. It tests the underlying scope
through `di.ScopeOwner`, never interface equality or the guarded resolver facade.
Foreign and non-DI holders return `execution.ErrScopeOwner`. With a factory that
implements only `di.ScopeFactory`, ownership cannot be verified; do not invent an
ownership comparison. `ResourcesFrom` checks the optional ownership capability on
new scopes too.

Arc also honors the optional `di.ContextChecker`, so wrapping a guarded DI scope
under a different principal or tenant cannot recapture its authority. Borrowing
never transfers disposal ownership or keeps a separately closed provider alive.

## Zero-container guarantee

No root-module runtime package depends on
`github.com/cratis/fundamentals.go/dependencyinjection/container`. Container use in
external tests or an explicitly designated DI example is opt-in. The compiled
`examples/nocontainer` source imports **no** `dependencyinjection` package and
executes authorization, validation, and scoped plain resources.

The small, standard-library-only DI contract package may occur transitively via
`execution`, including from authorization and validation. This guarantee excludes
a container implementation, not those contracts. CI checks the runtime dependency
graph and the example's direct imports with
`python3 scripts/check-no-container.py`; ordinary tests verify the example output.

## Migrate from the unreleased services API

The Arc `services` package and `execution.Run` are removed without compatibility
wrappers. Replace registrations with Fundamentals DI registrations, replace
`Run(ctx, provider, ...)` with
`RunWithResources(ctx, ResourcesFrom(provider), ...)`, and replace
`services.Resolve[T]` in operation callbacks with `execution.Resolve[T]`.
Callbacks now receive `*execution.Scope`, not a container scope. Callback panics
become inspectable `execution.PanicError` diagnostics joined with cleanup failures,
rather than being re-panicked. For plain wiring, skip DI altogether and keep using
`ResourcesAs[T]`.
