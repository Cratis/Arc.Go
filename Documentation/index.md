---
title: Arc for Go
description: Use Cratis Arc's foundation contracts from idiomatic, dependency-free Go packages.
---

<!-- Copyright (c) Cratis. All rights reserved. -->
<!-- Licensed under the MIT license. See LICENSE file in the project root for full license information. -->

Arc for Go is the Go port of Cratis Arc's HTTP CQRS framework. Without these
foundation packages, each application must recreate Arc's routes, scalar codecs
and result envelopes; with them, you share those contracts explicitly.

**Start with [your first running application](backend/go/core/getting-started.md).**
Go 1.26 or later is required. This experimental port supports backend pipelines,
snapshot and observable HTTP hosting, identity, and protected discovery. Typed Go
adapters and bounded TypeScript model/command/snapshot/observable-query proxies are
available, alongside optional Chronicle integration and MongoDB snapshots and
source-only watches. The generated observable Node case exercises default
WebSocket hub/Delta behavior with an independent collection consumer. A separate
bounded mounted React lane executes real generated hook reconstruction, cache
reuse and joined cleanup with explicit WebSocket/Delta providers. This is not
browser/DOM/StrictMode or full Arc-wrapper parity; suspense, reconnect, server
paging, setter correctness and 30-second expiry remain unverified. MongoDB watches
require explicit close/join/reopen after terminal loss; see the [Partial provider
profiles and limits](parity.md). Opaque provider-source generation, Chronicle
watches, and OpenAPI remain unsupported.
Constructing metadata or a result alone does not execute business code; the
application builder compiles the HTTP endpoints.

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
The planned release series is v0.x; experimental APIs may change between minor
releases. See the [contribution guide](../CONTRIBUTING.md) and
[release policy](releases.md). Foundation fixtures and Go-owned hosting tests do not certify full-product,
paired .NET or browser parity. Central documentation-site integration remains separate.
