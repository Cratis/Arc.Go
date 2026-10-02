---
title: Tenant selection and membership
description: Select a tenant from explicit request inputs and enforce membership as a separate security boundary.
---

A tenant header tells you where a caller wants to work, not whether they belong
there. You select a tenant with `tenancy.NewResolver(options)`, install it with
`WithTenant`, and independently enforce any configured Membership. Arc.Go provides
these contracts but no HTTP middleware, database routing or pipeline yet.

## Choose one selector

Zero Options selects header tenancy. Empty configurable names use the defaults:

| Strategy | Input | Default |
| --- | --- | --- |
| `Header` | Request header | `x-cratis-tenant-id` |
| `Query` | Query-string parameter | `tenantId` |
| `Claim` | First matching authenticated principal claim | `tenant_id` |
| `Subdomain` | `Request.Host`, else configured header | Requires BaseDomain |
| `Fixed` | Deployment FixedID | `development` when zero |

This excerpt assumes imports for `tenancy` and an operation context `ctx`:

```go
resolver, err := tenancy.NewResolver(tenancy.Options{Strategy: tenancy.Fixed})
```

Inspect `err`, then call `resolver.Resolve(ctx, request)`. Fixed works with a nil
request. Claim reads only authenticated typed principal metadata and can also work
without a request. An unauthenticated principal returns NotSet even when it has a
tenant claim; this is stricter than C#'s claim selector. Other selectors return
NotSet for a nil request. Missing input
returns NotSet; header/query selectors reject multiple values with
`ErrAmbiguousSelection`. Malformed query encoding and invalid tenant text fail
rather than silently selecting a partial value.

Construction copies configuration and rejects invalid strategies/names with
`ErrInvalidOptions`. Built-in resolvers are immutable and concurrent-safe. Custom
`ResolverFunc` callbacks borrow request data and must honor cancellation. There is
no development-mode override, automatic selector chain or membership catalog.

## Match subdomains safely

BaseDomain must contain at least two valid ASCII DNS labels and must not be an IP
literal. Case, surrounding ASCII dots and valid numeric ports are normalized.
Exactly one valid label before the suffix selects the tenant; a deep subdomain,
unrelated host, malformed ASCII label or IP falls back to the configured header.
Ports must fit uint16, DNS labels must fit 63 bytes, and names must fit 253 bytes.

Use Request.Host, never Forwarded or X-Forwarded-Host. This dependency-free surface
supports ASCII and already-punycoded names only. Unicode host/base-domain input
returns `ErrUnsupportedHost`; it does not silently fall back. C# normalizes IDNA;
full Unicode support is deferred.

## Enforce membership independently

`Require(id)` rejects only NotSet with `ErrNotSet`; named Default satisfies it.
[Execution metadata](../core/execution-context.md) describes sentinel codecs and
explicit tenant context ownership.

`Membership.Authorize(ctx, principal, tenant)` returns `(bool, error)`; use an
ordinary value or `MembershipFunc`. Selection never invokes it. Future hosting and
pipelines must enforce configured membership after selection even for public
operations. An authorization declaration's AllowAnonymous does not bypass it.
Do not treat correlation, a selected tenant ID, display details or an arbitrary
claim as membership approval.

Tenant selection is separate from Chronicle namespace isolation. See the
[parity ledger](../../../parity.md) for the supported subset and stricter input
handling.
