---
title: Arc for Go
description: Arc.Go provides Go commands, queries, and snapshot and observable HTTP hosting for Arc applications; it is a partial port, not full parity with C# Arc.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Arc for Go is the Go port of Cratis Arc's HTTP CQRS framework. Without these
foundation packages, each application must recreate Arc's routes, scalar codecs
and result envelopes; with them, you share those contracts explicitly.

**Start with [your first running application](backend/go/core/getting-started.md).**
Go 1.26 or later is required. The Go port supports backend pipelines,
snapshot and observable HTTP hosting, identity, and protected discovery. Typed Go
adapters and bounded TypeScript model/command/snapshot/observable-query proxies are
available, alongside optional Chronicle integration and MongoDB snapshots and
source-only watches. Generated observable clients use WebSocket hub/Delta
transport. React hooks support reconstructing query results, reusing cached
queries and cleaning up subscriptions with explicit WebSocket/Delta providers.
This is not full Arc-wrapper support: browser/DOM/StrictMode behavior, suspense,
reconnect, server paging, setter correctness and 30-second cache expiry remain
unverified. MongoDB watches require explicit close/join/reopen after terminal
loss; see the [Partial provider
profiles and limits](parity.md). Opaque provider-source generation, Chronicle
watches, and OpenAPI remain unsupported.
Constructing metadata or a result alone does not execute business code; the
application builder compiles the HTTP endpoints.

For feature coverage and differences from Arc on .NET, consult the
[parity map](parity.md). Contributor verification details are in the repository's
`CONTRIBUTING.md`.

## Inspect a command route and result

This is the body of the [compiled Example](../example_test.go). It uses packages
`metadata`, `concepts` and `commands` under `github.com/cratis/arc.go`, plus `fmt`.

```go
command := metadata.Command{Type: metadata.TypeName{
    Namespace: "Tasks.Registration", Name: "RegisterTask",
}}
catalog := metadata.Catalog{
    Version: metadata.Version, Commands: []metadata.Command{command},
}
routes, err := metadata.Resolve(catalog, metadata.DefaultOptions())
if err != nil {
    fmt.Println(err)
    return
}
id, err := concepts.ParseUUID("00112233-4455-4677-8899-aabbccddeeff")
if err != nil {
    fmt.Println(err)
    return
}
result := commands.WithResponse(id, struct {
    ID string `json:"id"`
}{ID: "a1"})
response, present := result.Response()
fmt.Println(routes[0].Method, routes[0].Path)
fmt.Println(result.StatusCode(), response.ID, present)
```

Output:

```text
POST /api/tasks/registration/register-task
200 a1 true
```

## Choose a contract

- [Concepts and JSON values](backend/go/concepts/index.md): UUIDs, dates, ticks and missing/null input.
- [Command results](backend/go/commands/results.md): success, rejection and response presence.
- [Query results](backend/go/queries/results.md): readiness, paging and change-set values.
- [Validation findings](backend/go/validation/index.md): severities and machine-readable reasons.
- [Routes and stable identities](backend/go/configuration/routing.md): explicit metadata and collision diagnostics.
- [Generate adapters and proxies](backend/go/generation/index.md): typed Go wiring and bounded TypeScript publication.
- [Parity ledger](parity.md): pinned sources, executable evidence and deliberate differences.

## Publication

After the first tagged release, use `go get github.com/cratis/arc.go@latest`.
The planned release series is v0.x; APIs may change between minor
releases. See the [contribution guide](../CONTRIBUTING.md) and
[release policy](releases.md). Foundation fixtures and Go-owned hosting tests do not certify full-product,
paired .NET or browser parity. Central documentation-site integration remains separate.
