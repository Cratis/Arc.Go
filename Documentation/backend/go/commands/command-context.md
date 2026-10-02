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

## Respect callback lifetimes

`Invocation.Scope()` is non-closing and expires at callback return. Its bound `Pipeline()` joins the current command owner; retained, concurrent, changed-security, and ancestor-frame reuse fail. Use a child's own Invocation for further nested commands. Context values never store a provider, scope, resolver, or execution owner on behalf of the framework. See [execution scopes](command-execution-scopes.md) for nesting and completion.
