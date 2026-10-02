---
name: go-http-handlers
description: Implement Arc.Go net/http command, query, and observable-query handlers with compatible routes, JSON, middleware, streaming, and graceful shutdown. Use for binding, transport errors, SSE/WebSocket, and server lifecycle.
---

# Arc Go HTTP handlers

Read `Documentation/project-context.md`, `.cratis/ai/rules/go.md`, and
`.cratis/ai/rules/go-cratis-parity.md`. Derive behavior from
`../Arc/Source/DotNET` and TypeScript consumers in `../Arc/Source/JavaScript`.
Use `go-concurrency` for subscription ownership and `go-testing` for checks.

## Workflow

1. Record the original route, verb, binding/presence semantics, response envelope,
   status/error mapping, authorization, and observable protocol before coding.
   Arc is CQRS, not inherently event sourcing; Chronicle integration is optional
   at the application boundary even though Arc.Go depends on Chronicle.Go.
2. Compose with `http.Handler` and middleware of shape
   `func(http.Handler) http.Handler`. Prefer Go 1.22+ `ServeMux` method/path
   patterns and `Request.PathValue` where they match the established routes.
   GET also matches HEAD; check wildcard, redirect, and conflict behavior.
3. Separate binding, validation, authorization, command/query execution, and
   response writing. Propagate `r.Context()` and select the tenant before dispatch.
   Do not perform domain work after a response has already been committed.
4. Bound bodies with `http.MaxBytesReader`; validate content type. Decide unknown
   fields and trailing JSON explicitly against parity, not personal preference.
   Decode exactly one value and distinguish absent, null, empty, and zero.
5. Preserve camelCase JSON tags, concepts/UUIDs, date precision, enums, numeric
   ranges, error envelopes, paging, and sorting. Do not add `omitempty` blindly.
6. For ordinary JSON responses, encode before writing headers/status so encoding
   failures do not produce half a success response. Use bounded payloads; streaming
   has a different error contract once headers are sent.
7. Order middleware deliberately: recovery/diagnostics, identity/tenant selection,
   authentication/authorization, dispatch, and completion logging as appropriate.
   Wrappers must preserve streaming/hijacking capabilities, including `Unwrap`
   for `http.ResponseController`; avoid pretending optional interfaces exist.
8. For observables, implement the original SSE or WebSocket framing, handshake,
   errors, heartbeat, and reconnect semantics; they are not interchangeable.
   Authenticate subscriptions, enforce origin policy, bound message sizes/queues,
   define slow-consumer behavior, and propagate disconnect cancellation.
9. SSE needs correct headers/framing and flushing. WebSockets require a vetted
   implementation plus explicit reader/writer ownership and ping/pong/close policy.
   Do not write to a `ResponseWriter` after `ServeHTTP` returns.
10. Set server header/idle/body/write limits appropriate to unary and streaming
    paths; one short global write timeout can kill legitimate subscriptions.
    Applications own listeners/signals unless the API explicitly takes ownership.
11. On shutdown, stop admission, cancel/join subscriptions, call `Server.Shutdown`
    with a fresh bounded context, and handle its error. Shutdown does not close
    hijacked WebSockets; track and close those explicitly. A canceled request or
    signal context must not be reused as the shutdown budget.

## Small example: encode before committing

Transport helper only; actual Arc handlers must provide the original envelope
and error mapping. It reports write errors but cannot undo a committed response.

```go
package transport

import (
 "encoding/json"
 "fmt"
 "net/http"
)

// WriteJSON encodes value before committing status and JSON headers.
func WriteJSON(w http.ResponseWriter, status int, value any) error {
 body, err := json.Marshal(value)
 if err != nil {
  return fmt.Errorf("encode response: %w", err)
 }
 w.Header().Set("Content-Type", "application/json")
 w.WriteHeader(status)
 if _, err := w.Write(append(body, '\n')); err != nil {
  return fmt.Errorf("write response: %w", err)
 }
 return nil
}
```

The caller must not blindly call `http.Error` on every helper error: a write
failure occurs after headers are committed. Establish boundary handling that
knows whether it can still send the original contract's failure envelope.
Do not use this helper for HEAD, no-body statuses, or streaming without adapting
its behavior to those contracts.

## Verify and stop conditions

Use recorder tests for binding/status/JSON and `httptest.Server` for transport,
HEAD, redirects, cancellation, flushing, and middleware behavior. Test invalid
JSON, multiple values, oversized bodies, unauthorized/other-tenant access,
slow consumers, disconnects, and shutdown with active observables/WebSockets.

Use fixtures consumed by the existing TypeScript client where practical; add
Chronicle integration tests at the adapter boundary. Run required gates and
update `Documentation/parity.md`. Stop if framing, route, or response contracts
remain unresolved; a plausible HTTP API is not sufficient parity.

## References

- [ServeMux routing](https://go.dev/blog/routing-enhancements)
- [net/http](https://pkg.go.dev/net/http)
- [httptest](https://pkg.go.dev/net/http/httptest)
- [Context](https://pkg.go.dev/context)

Original workflow/example informed by official Go documentation and Cratis
contracts; no third-party handler code is copied.
