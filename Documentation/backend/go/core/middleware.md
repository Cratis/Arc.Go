---
title: Compose HTTP middleware
description: Add standard net/http middleware without bypassing Arc ingress or pipeline authorization.
---

Use `builder.Use` for middleware of type `func(http.Handler) http.Handler`.
The first registration is outermost. Constructors run once during Build, in
reverse wrapping order; request callbacks run only after startup. Nil middleware,
nil returned handlers and constructor panics fail composition.

## Request ordering

Arc establishes correlation and receipt, admits the request, rejects noncanonical
paths and installs framework cache policy before application middleware runs.
Authentication and tenant resolution run inside that middleware, before endpoint
execution. Unknown routes and method mismatches do not challenge credentials.
Raw handlers receive authenticated/anonymous principal and resolved tenant metadata.
An outer host's principal is used only with `authentication.HostPrincipal()`.
The HTTP receipt timestamp is ordinary metadata here. Only the immediate endpoint
pipeline call receives its forwarding marker; backend commands/queries started
by middleware, raw handlers or providers capture fresh receipts.

Middleware is trusted code. Do not consume a command/QUERY body, remove privacy
headers or bypass pipeline authorization. Register owned startup work with
`AddLifecycle`, not inside a middleware constructor.

## Diagnostics and response capabilities

A nil logger is silent. An injected `slog.Logger` receives bounded route templates,
method, correlation, status and duration, never request bodies or argument values.
Pipeline stages own their errors; hosting owns transport/provider failures.
Ingress callback errors log only the error: client-attributable selector failures
use Warn, server faults use Error. Authentication failure reasons and request
headers are not logged. Authentication adapters must never include or wrap token
text or credentials in returned errors, which the host may log.

Request panics are logged at Error without exposing the panic value to clients.
Arc does not rewrite committed responses, and propagates `http.ErrAbortHandler`.

Response observation exposes `Unwrap` for `http.ResponseController`, and forwards
Flusher, Hijacker and Pusher only when the underlying writer supports them. Raw
handler streaming does not become an Arc observable subscription or acquire Arc
connection cleanup ownership.
