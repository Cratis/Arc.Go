---
title: Authentication handlers
description: Authenticate Arc Go HTTP requests with ordered credential adapters, keeping display cookies and unverified forwarded headers outside the trust boundary.
---

Authentication decides who called; [authorization](../authorization/index.md)
decides what they may do. Arc.Go applies an ordered handler chain at HTTP ingress, not a built-in token
verifier. You supply verified adapters as ordinary
values or `HandlerFunc` closures.

Use this page to carry a host-verified identity into a command or query. If your
application needs to verify bearer tokens itself, follow the [JWT
recipe](../recipes/jwt.md) instead; it provides a handler to register with Arc.Go.

## Compose a trusted chain

`HostPrincipal` reads
only `identity.PrincipalFrom(ctx)`. Installing a principal is a trusted operation:
`identity.NewPrincipal` does not check credentials. Never construct it from an
unverified token payload, display cookie or caller-authored identity header.

For context installation, see [execution metadata](../core/execution-context.md).

This excerpt assumes imports for `authentication`, `identity`, `context` and
`net/http`, plus a request already authenticated by your host:

```go
chain, err := authentication.New(authentication.HostPrincipal())
```

Inspect `err`, then call `chain.Authenticate(ctx, request)` to use the chain
directly. Constructing this standalone chain does not register it with application
hosting.

For hosted requests, supply the handlers in `arc.Options.Authentication` when
calling `arc.NewBuilder`; `Build` constructs the chain. No handlers are configured
by default, so include `authentication.HostPrincipal()` when a trusted host has
installed a principal. When the chain returns without an error or credential
failure, hosting installs its principal on the incoming request context with
`identity.WithPrincipal`, then dispatches the request to the command or query
pipeline, which captures that principal for invocation. See [hosting
options](../configuration/hosting.md) for the configuration boundary.

| Result | Chain action |
| --- | --- |
| `Anonymous()` or zero | Continue to the next handler |
| `Authenticated(principal)` | Stop; anonymous principals are rejected at construction |
| `Failed(reason)` | Stop; later handlers cannot rescue rejected credentials |
| Callback error | Stop with the original error, not credential rejection |

`New` rejects nil and typed-nil handlers, copies the registration slice and
borrows the handlers. Shared handlers must support concurrent calls. Zero `Chain`
is empty. Chain exhaustion remains anonymous.

Supplied context is installed on a request clone; context, headers and URL are
isolated from the caller. The body is shared, not cloned: handlers must not read
`Body`. Other request data is not guaranteed isolated, and request data is borrowed
only for the call.

Cancellation is checked before and after callbacks; callbacks must cooperate.

## Handle failures at ingress

`Result.Principal()` returns the trusted snapshot and presence;
`Result.Failure()` returns immutable diagnostic data or nil. A `Failure` wraps
`ErrFailed`; `Reason()` is local diagnostics, while its error text is generic.
Other categories are `ErrInvalidHandler`, `ErrInvalidPrincipal` and
`ErrInvalidRequest`.

Application hosting maps explicit credential failure to 401 even for a public
operation. An exhausted anonymous chain proceeds to authorization; ordinary
denial is 403, including anonymous role denial. This differs from native C# Core
middleware's blanket credential requirement and anonymous failure exemption.
The standalone chain writes no response; the application owns that boundary.
Callback errors are server faults and log at Error. Never include or wrap token
text, credentials or header values in those errors. Use `Failed(reason)` for
credential rejection; ingress never logs the reason. Client-attributable tenant
selection errors log at Warn and return malformedRequest validation findings.

After configuring authentication, [declare operation
requirements](../authorization/index.md) to control which callers may invoke each
command or query. For a 403, inspect those declarations and the principal's roles;
authentication alone does not grant permission.

Forwarded identity headers are an alternative to a host-installed principal, not
a required next step. There is no automatic cookie, Basic or JWT adapter.
Only trust forwarded identity when authenticated ingress strips caller-supplied
headers, replaces them, and prevents direct access to the backend.

Explicitly register
`authentication.MicrosoftIdentityPlatform` with `MicrosoftIdentityOptions` to
accept unsigned forwarded headers only when `TrustForwardedIdentityHeaders` is
true. Repeated headers and oversized/malformed principals are rejected.
When trust is off, headers are ignored and requests remain anonymous. Set the
optional `MicrosoftIdentityOptions.Logger` to receive a Warn diagnostic on the
first such request across all adapter instances in the process. No header values
are logged; nil disables diagnostics. See the
[parity ledger](../../../parity.md) for the source-derived ingress differences.
