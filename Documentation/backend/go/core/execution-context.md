---
title: Execution metadata
description: Carry trusted identity, tenant, correlation and receipt values through explicit Go contexts.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

A job should not accidentally inherit the user or tenant of the request that
scheduled it. You carry immutable metadata in explicit contexts and choose the
actor for independently initiated work. These are foundation APIs, not command
pipelines, authentication, authorization, or HTTP hosting.

## Carry identity and tenant values

`identity.NewPrincipal` copies trusted application input. It does **not** verify
credentials. Only a verified adapter or trusted application code should construct
an authenticated principal. A nonempty `AuthenticationType` determines
`IsAuthenticated`; an ID or arbitrary claim never authenticates the caller.

Roles are explicit, exact, case-sensitive strings. `HasRole` also requires
authentication. Claims retain duplicate types and input order; `Claim` returns
the first exact match. `Roles` and `Claims` return independent slices. `Equal`
compares every captured field and ordered slice content.

`identity.System("jobs")` creates an authenticated `[System]` actor with the
supplied roles and C#-compatible subject, name, and role claims. It is not an
authorization bypass: it does not acquire roles you did not supply.

`tenancy.ParseID` accepts arbitrary tenant text, preserves case and interior
spaces, and rejects control characters, invalid UTF-8, and surrounding whitespace.
It does not impose UUID, DNS, or database-name restrictions. Invalid text and
non-string JSON return `tenancy.ErrInvalidID` without exposing rejected text or
changing the receiver. JSON null is rejected.

| Value | `String()` | `IsSet()` | `IsDefault()` |
| --- | --- | --- | --- |
| Zero or `ParseID("")` or `ParseID("[NotSet]")` | `[NotSet]` | false | true |
| `tenancy.Default()` | `Default` | true | true |
| Other parsed text | Original text | true | false |

NotSet and named Default remain unequal. A tenant ID alone proves no membership.

`WithPrincipal` and `WithTenant` derive contexts without changing parents.
`PrincipalFrom` and `TenantFrom` return a presence flag: an explicitly installed
anonymous principal or NotSet tenant shadows a parent and still reports present.
There are no package-global current values or restoration leases.

## Correlation and receipt

`correlation.ID` is a type alias of `concepts.UUID`, so existing command/query
result fields need no conversion. `correlation.DefaultHeader` is
`X-Correlation-ID`; no HTTP handler is installed by this package.

| Function | Behavior |
| --- | --- |
| `correlation.Parse(text)` | Trim whitespace, parse a nonzero dashed UUID; invalid input returns `ErrInvalidID` |
| `correlation.Normalize(text)` | Keep valid input; replace missing, malformed, or zero input with a cryptographic version-4 UUID |
| `correlation.Resolve(ctx, text)` | Prefer valid text, then a nonzero context ID, then generation |
| `correlation.FromContext(ctx)` | Read only; return zero when absent |
| `correlation.WithID(ctx, id)` | Install an explicit ID; zero shadows parents |

Generated IDs preserve generation failures. Correlation conveys no authorization
or ownership authority. Other C# Guid input spellings are not supported.

`execution.WithReceivedAt` stores a UTC receipt without a monotonic component;
`ReceivedAt` returns the value and its presence. Nested overrides leave parents
unchanged. `Capture` copies only Arc metadata, never a scope, provider, request,
or cancellation ownership.

`NewContext(ctx, metadata)` installs the supplied principal and tenant even when
they are anonymous/NotSet. Zero correlation generates a UUID; zero receipt uses
`time.Now`. Callers needing an injected clock supply its receipt explicitly.
The new context retains the parent's cancellation and deadlines.

## Run an independent job

Build a [service provider](services.md), then call `execution.Run` using an
application/job lifetime context. This excerpt assumes `jobCtx` and `provider`
are already available and imports `context`, `execution`, `identity`, `services`,
and `tenancy`:

```go
err := execution.Run(jobCtx, provider, execution.Metadata{
    Principal: identity.System("jobs"),
    Tenant:    tenancy.Default(),
}, 0, func(ctx context.Context, scope *services.Scope) error {
    _, err := services.Resolve[string](ctx, scope)
    return err
})
```

Register `string` before this call. Inspect the returned `err`. Run invokes the
callback synchronously in a fresh scope, always attempts scope cleanup, and joins
callback, cancellation, and cleanup errors. It creates no goroutine. Callback
panics are re-panicked after cleanup; the original panic is not replaced by a
cleanup error. Cleanup errors cannot be returned on that panic path.

Zero cleanup timeout means 30 seconds. Negative timeouts and nil providers or
callbacks return `execution.ErrInvalidArgument`. Cleanup uses a bounded
`context.WithoutCancel` child: it preserves metadata but does not inherit caller
cancellation. Deadlines are cooperative, not forced termination. Do not detach a
request context to give accidental background work an unlimited lifetime.

Scope cleanup releases resources; it never completes or commits command effects.
For bounded compatibility claims and remaining pipeline work, read
[the parity ledger](../../../parity.md).
