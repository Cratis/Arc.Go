---
title: Command context and keys
description: Read immutable command snapshots and supply copied context values or an explicit command key.
---

A handler needs to know which command frame it belongs to without borrowing an ambient service provider. `commands.ContextFrom(ctx)` returns a metadata snapshot; `Invoke` and `Prepare` additionally receive an expiring `Invocation` for stage-local facilities.

## Read a snapshot

`CommandContext` exposes the descriptor, decoded command, correlation, receipt, principal, tenant, context values, explicit caller allowance, validation-only flag, selected response, and resolved key. Descriptor and membership collections are copied. Command, response, and application objects inside values remain borrowed.

Each callback gets a current immutable snapshot. `Invocation.SetValue` changes membership for later callbacks; it does not mutate a snapshot already read from context. Names are case-insensitive. Security metadata and ownership are not writable context entries.

## Supply values before filters

`AddContextValuesProvider` registers named lazy providers. They run before authorization and validation, so they are trusted extension work and must not eagerly construct protected dependencies. `NewContextValues` copies map membership and rejects ambiguous case-insensitive names. Later providers overwrite earlier entries.

`Entries` returns independent membership, normalized to lower-case names. This is not a deep clone. Shared mutable application objects still require explicit ownership and synchronization.

## Resolve an explicit key once

Key precedence is:

1. A provider-written `resolvedKey` entry, including an authoritative empty string.
2. Custom `KeyResolver` registrations, taking the first usable nonempty key.
3. `KeyProvider.GetKey()` on the decoded command.
4. An explicit `WithKey` selector or one `arc:"key"` field.

A field named Id or a UUID value is not a key convention. Multiple key tags fail configuration. `arc:"identity"` is read-model metadata and never a command key. The key is provider-neutral; it does not imply Chronicle event-source identity or authorization.

## Keep extension state out of context values

An integration can create a private `NewStateKey[T]` and use `RootState`/`SetRootState` for the bound command tree, or `FrameState`/`SetFrameState` for one command's stages. Root state is not shared by independent executions, even when they borrow the same operation scope. Frame state survives Provide to Handle but is not inherited by children or siblings. Membership is discarded when its lifetime ends.

Every access checks callback admission, principal, tenant, correlation, and the current frame. Values are borrowed, not deep-copied or automatically disposed. Guard mutable contents yourself, and recheck `Invocation.Execution().Check(ctx)` before using retained mutation capabilities. Keep keys that identify completion owners private. No initializer runs under a framework state lock.

`ParentCommandContext(ctx)` exposes a copied parent metadata snapshot for nested causation, not a parent invocation or owner. Command/value objects inside that snapshot remain borrowed immutable data.

## Resolve command-side read models

`RegisterReadModelProvider[M]` binds an exact non-pointer struct type to a command dependency resolver. Register on `builder.Commands()` when composing an Arc application. `DeclaredReadModel` beats `FallbackReadModel`; an application's `OverrideReadModel` beats both. Duplicate claims at the same level fail, even if another level overrides them. Unlike C# replacement-order behavior, two declared owners never silently replace each other.

Generated and handwritten adapters use the same helpers: `ResolveReadModel[M]` retains `Exists`, `RequireReadModel[M]` rejects absence as `dependencyUnavailable`, and `ReadModelOrNil[M]` returns nil for an absent instance. Missing or blank keys are `malformedRequest`, even for optional reads. A present zero-valued model is present; reader and release failures remain errors. This is not a public query invocation or an extra authorization contract.

The selected provider owns materialization, release, and caching. Provider-specific frame caches must include the key, coordinates, and read mode when those can vary. Successful reads can be reused between Provide and Handle; transient failures must not become cached absence. Validation filters can use these helpers without starting a transaction or calling Provide/Handle. The executable `ExampleRegisterReadModelProvider` demonstrates the provider-neutral path. The optional [Chronicle integration](../chronicle/index.md) supplies a separate SDK-backed provider.

## Respect callback lifetimes

`Invocation.Scope()` is non-closing and expires at callback return. Its bound `Pipeline()` joins the current command owner; retained, concurrent, changed-security, and ancestor-frame reuse fail. Use a child's own Invocation for further nested commands. Context values never store a provider, scope, resolver, or execution owner on behalf of the framework. See [execution scopes](command-execution-scopes.md) for nesting and completion.
