---
title: Authentication handlers
description: Compose trusted credential adapters without treating display cookies or forwarded headers as proof of identity.
---

Authentication decides who called; [authorization](../authorization/index.md)
decides what they may do. Arc.Go supplies an ordered handler chain, not HTTP
middleware or a credential verifier. You supply verified adapters as ordinary
values or `HandlerFunc` closures.

## Compose a trusted chain

This excerpt assumes imports for `authentication`, `identity`, `context` and
`net/http`, plus a request already authenticated by your host:

```go
chain, err := authentication.New(authentication.HostPrincipal())
```

Inspect `err`, then call `chain.Authenticate(ctx, request)`. `HostPrincipal` reads
only `identity.PrincipalFrom(ctx)`. Installing a principal is a trusted operation:
`identity.NewPrincipal` does not check credentials. Never construct it from an
unverified token payload, display cookie or caller-authored identity header.

| Result | Chain action |
| --- | --- |
| `Anonymous()` or zero | Continue to the next handler |
| `Authenticated(principal)` | Stop; anonymous principals are rejected at construction |
| `Failed(reason)` | Stop; later handlers cannot rescue rejected credentials |
| Callback error | Stop with the original error, not credential rejection |

`New` rejects nil and typed-nil handlers, copies the registration slice and
borrows the handlers. Shared handlers must support concurrent calls. Zero `Chain`
is empty. Chain exhaustion remains anonymous. Supplied context is installed on a
request clone; context, headers and URL are isolated from the caller. The body is
shared, not cloned: handlers must not read `Body`. Other request data is not
guaranteed isolated, and request data is borrowed only for the call. Cancellation
is checked before and after callbacks; callbacks must cooperate.

## Handle failures at ingress

`Result.Principal()` returns the trusted snapshot and presence;
`Result.Failure()` returns immutable diagnostic data or nil. A `Failure` wraps
`ErrFailed`; `Reason()` is local diagnostics, while its error text is generic.
Other categories are `ErrInvalidHandler`, `ErrInvalidPrincipal` and
`ErrInvalidRequest`.

Future hosting must map explicit credential failure to 401 even for a public
operation. An exhausted anonymous chain proceeds to authorization; ordinary
denial is 403, including anonymous role denial. This differs from native C# Core
middleware's blanket credential requirement and anonymous failure exemption.
No status or response is written by these foundations.

There is no automatic cookie, Basic, JWT, or Microsoft forwarded-header adapter.
Only trust forwarded identity when authenticated ingress strips caller-supplied
headers, replaces them, and prevents direct access to the backend. See the
[parity ledger](../../../parity.md) for pending hosting and pipeline work.
