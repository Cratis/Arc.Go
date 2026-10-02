---
title: Services and scopes
description: Register exact Go service types with validated lifetimes and explicit resource ownership.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

A long-lived service must not capture the first request's user or tenant. You
register explicit dependencies, validate lifetime paths before execution, and
resolve request-bound services inside an isolated scope. This is a foundation
container, not CLR discovery, root application composition, or command completion.
Use ordinary constructors when you do not need shared scope ownership.

## Register and build

A zero `services.Registry` is ready to use. It is a single-owner mutable builder;
providers and ordinary scopes support concurrent use. Keys identify exact Go
types: interfaces, concrete values, and pointers are separate registrations.
`KeyFor[T]()` returns the key and `Key.String()` supplies its diagnostic identity.
No assignability search, constructor discovery, or pointer conversion occurs.

This excerpt assumes a `registry` variable of type `services.Registry` and imports
`context` and `services`:

```go
err := services.Bind(&registry, services.Scoped,
    func(ctx context.Context, scope *services.Scope) (int, error) {
        value, err := services.Resolve[string](ctx, scope)
        return len(value), err
    }, services.KeyFor[string]())
```

Inspect `err`, and register `string` with `BindValue` or `Bind` before building.
Declare every direct resolution edge; dependency lists are copied. Factories must
not hide request-bound dependencies inside captured closures.

| Lifetime | Cache | Owner |
| --- | --- | --- |
| `Singleton` | Once per provider | Provider, including its transient dependencies |
| `Scoped` | Once per scope | Scope |
| `Transient` | No cache | Scope, or provider when reached from a singleton |

`Bind` owns successfully produced values. `BindValue` registers a borrowed
singleton that Arc **never closes**. Do not register the same owned object under
multiple keys: object identity is not deduplicated during cleanup.

Registration rejects nil factories, typed-nil values, invalid lifetimes, zero
keys, duplicate bindings, and duplicate dependency declarations. `Build` validates
missing edges, cycles, and every transitive singleton-to-scoped path. Singleton →
transient is allowed; singleton → transient → scoped is not. Diagnostics follow
sorted package-qualified identities. Build invokes no factory, cleanup, I/O, or
goroutine. An empty registry is valid.

Successful Build freezes further registration/build with `ErrFrozen`. Failed
Build leaves the registry editable, so you can supply a missing binding and retry.
Providers and scopes require construction; their zero values are invalid.

## Resolve in an isolated scope

Call `provider.NewScope(ctx)`, then `services.Resolve[T](ctx, scope)`. Resolve
returns the exact registered type. `Contains(key)` inspects registrations without
constructing services; `Owns(scope)` identifies ordinary scopes from that provider,
including closed scopes. Factory views are not ordinary ownership handles.

A scope captures principal and tenant values **and presence**, never the context
itself. Both Resolve and `CheckContext` reject replacements with
`ErrContextMismatch`, even replacing absent metadata with explicit anonymous or
NotSet values. Changing correlation or receipt is allowed. Read
[execution metadata](execution-context.md) for typed context accessors.

Singleton factories retain cancellation and deadlines but receive no context
values: no principal, tenant, receipt, correlation, or custom request metadata.
Their transient dependencies also run under this value-free context. Application
closures can still capture hidden state; graph checks cannot detect that misuse.

Factories receive restricted views authorizing only declared direct dependencies.
Undeclared edges fail before the dependency is constructed. Views expire when the
factory returns; retaining one returns `ErrFactoryScopeExpired`. Overlapping
resolutions on one view return `ErrConcurrentFactoryUse`. Resolve dependencies
sequentially inside factories. Ordinary handles support concurrent resolution;
this does not make your resolved application services thread-safe.

Successful singleton/scoped instances are cached once. Concurrent callers share
an in-flight attempt. Each waiter may cancel without canceling the creator. An
attempt's failure reaches current waiters; a later call can retry, but Arc never
retries automatically. Cancellation is checked around factory callbacks. Factory
panics become inspectable `services.Error` values and release waiters.

## Close owned resources

Close the scope when its operation ends, and close the provider at application
shutdown. Stop and join application handlers first: Close waits for admitted
**resolutions**, not arbitrary handler goroutines using their returned services.

Scopes release values in reverse successful creation order. Providers stop
admission, close remaining scopes in reverse opening order, then release root-owned
values in reverse creation order. Closed scopes unregister to avoid retention.
`services.Closer` receives the cleanup context; ordinary `io.Closer` is also
supported. The contextual check comes first; Go cannot overload the two different
`Close` signatures on one type.

Cleanup continues after errors and panics and combines failures with `errors.Join`.
A nonnil value returned with a factory error is immediately closed. Dependencies
successfully resolved before a parent fails remain owned until scope/provider
cleanup. Borrowed values are never disposed. Concurrent/repeated Close calls do
not execute cleanup twice, and completed cleanup results are retained.

If cancellation interrupts waiting before cleanup starts, the handle remains
closing and rejects new work. A later Close resumes. Once cleanup starts, every
remaining closer is attempted with the supplied context. Timeouts are cooperative:
Arc does not create detached cleanup goroutines or force a closer to terminate.
Closing a service scope never commits command effects.

## Inspect failures

Use `errors.Is` for categories and `errors.As` for `*services.Error`. Its operation,
key, copied path, cause, and panic payload are available locally. Error text does
not display service values, claims, callback error messages, or panic payloads.
Avoid logging sensitive diagnostic causes or panic values indiscriminately.

Categories cover invalid registration, duplicates, frozen builders, missing
services, cycles, captive lifetimes, undeclared edges, closed/invalid scopes,
context mismatch, expired/concurrent factory use, nil values, and callback panics.
Callback and cancellation causes remain inspectable through wrapping.

For a fresh bounded job scope, use [execution.Run](execution-context.md#run-an-independent-job).
[The parity ledger](../../../parity.md) records deliberate differences from C# DI.
