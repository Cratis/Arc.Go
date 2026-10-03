---
title: Identity details
description: Provide display data without changing trusted claims, roles or operation permissions.
---

You may need profile data for a frontend without giving that data authority over
a command. `identity.ProvideDetails` builds a caller-owned display View from a
trusted principal and a plain details provider. The built application maps `/.cratis/me` using the default or selected provider;
the standalone helper itself performs no transport work.

## Provide fresh details

This excerpt assumes imports for `identity` and `context`:

```go
provider := identity.DetailsProviderFunc[string](
    func(_ context.Context, value identity.Context) (identity.Details[string], error) {
        return identity.Details[string]{
            IsUserAuthorized: true,
            Value:            "Hello, " + value.Name(),
        }, nil
    })
```

Call `identity.ProvideDetails(ctx, provider)` after installing a verified principal.
It suppresses the callback for anonymous callers (`ErrUnauthenticated`), rejects
nil/typed-nil providers (`ErrInvalidProvider`), and rejects a false
IsUserAuthorized (`ErrDetailsDenied`). Every call invokes freshly; there is no
cookie or result cache. Callback errors propagate; cancellation is checked around
the callback. Shared providers must support concurrent calls.

`ContextFor(principal)` copies ordered claims and supplies `unknown` display
fallbacks for empty ID/name without changing Principal. `Context.Claims()` returns
independent slices. ID comes from the verified adapter's explicit subject, not an
arbitrary claim or forwarded header.

## Keep display data non-authoritative

| View field | Source |
| --- | --- |
| `id`, `name` | Display context |
| `isAuthenticated` | Trusted principal |
| `isAuthorized` | Details provider's verdict, not command/query admission |
| `roles` | Copied explicit principal roles; nil emits `[]` |
| `details` | Unwrapped application Value, including null |

`MarshalJSON` uses Arc's bounded traversal and camelCase model serialization.
Details is borrowed; do not mutate it while encoding. A malicious details DTO
with its own `roles` or `isAuthenticated` fields stays under `details` and cannot
change top-level roles or trusted context metadata. View itself is mutable
caller-owned output, never a principal you should install for authentication.

For operation permission use [authorization policies](../authorization/index.md).
For the immutable trusted snapshot use [execution metadata](../core/execution-context.md).

## Register a hosted provider

`arc.RegisterIdentityDetails(builder, name, provider)` borrows a concurrently
callable provider. `RegisterScopedIdentityDetails` accepts a lazy resource-scoped
factory and optional declared DI keys. Anonymous callers never activate it.
One custom provider is selected automatically; multiple providers require
`Identity.DetailsProvider`, and unknown selections fail Build.

`/.cratis/me` returns fresh unwrapped JSON, empty 401 for anonymous callers and
empty 403 for provider denial. Provider/cleanup failures suppress success with
empty 500. Legacy `.cratis-identity` cookies are expired, never decoded or issued.
Responses use `no-store, private` and merged `Vary: Cookie`.

See [schemas](../introspection/schemas.md) and [parity](../../../parity.md) for
bounded schema support and current-source cookie behavior.
