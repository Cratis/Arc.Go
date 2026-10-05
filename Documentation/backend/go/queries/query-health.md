---
title: Protected query health
description: Enable an opt-in, role-protected observable query that reports aggregate observable-query and hub connection counts.
---

Observable queries hold connections, subscriptions and source resources open. When
an instance misbehaves you want to know how many are opening, running, closing or
stuck in cleanup, without exposing who is connected or what they asked for. Arc.Go
offers that as an opt-in framework query at `/.cratis/queries/health`.

C# Arc's `Queries/QueryHealth.cs` and `QueryHealthTracker.cs` (Arc `7c1e780`) expose
a detailed health topology anonymously. Arc.Go deliberately differs. Health is
**absent by default in every environment, protected by administrative roles, and
aggregate only**. Status: **Partial**.

## Enable the endpoint

`ExampleQueryHealth` in `query_health_example_test.go` is the complete compiled
example. Supply at least one role:

```go
builder, err := arc.NewBuilder(arc.Options{
    QueryHealth: &arc.QueryHealthOptions{Roles: []string{"Operations"}},
})
if err != nil {
    panic(err)
}
application, err := builder.Build()
if err != nil {
    panic(err)
}
if err := application.Start(context.Background()); err != nil {
    panic(err)
}
// Backend code supplies a trusted identity. HTTP callers instead require an
// Authentication handler that verifies credentials and supplies this role.
ctx := identity.WithPrincipal(context.Background(), identity.System("Operations"))
result, err := queries.Perform[arc.QueryHealth](ctx, application.Queries(), arc.QueryHealthName, queries.Request{})
if err != nil {
    panic(err)
}
health, present := result.Data()
fmt.Println(result.IsSuccess(), present, len(health.Observations))
```

It prints `true true 0`. Run it with:

```bash
go test -run ExampleQueryHealth .
```

`Roles` is copied. It must contain at least one nonempty role without surrounding
whitespace and at most 512 bytes; otherwise `NewBuilder` fails. Any one matching
role on an authenticated principal grants access. Leaving `Options.QueryHealth` nil
registers nothing: the path returns 404, even in Development.

## Read it over HTTP

| Request | Result |
| --- | --- |
| `GET /.cratis/queries/health` with a permitted role | 200 and a query result envelope whose `data` is the current sample |
| `HEAD` with a permitted role | 202 with no body; like other observable HEAD requests it does not activate the source |
| `QUERY` | Same as GET when `Routes.EnableQueryHTTPMethod` is on (the default); 405 when it is off |
| `Accept: text/event-stream` | Server-Sent Events stream: the current sample, then one sample per second |
| Missing or wrong role | 403 with an unauthorized envelope; no state is read |

Every response, including streams, uses `Cache-Control: no-store, private`. The
health query is also available through the observable hubs under
`QueryHealth.ObserveHealth` (`arc.QueryHealthName`). Hub subscriptions without a
permitted role receive an `Unauthorized` message. The endpoint is excluded from
discovery catalogs.

You need an authentication handler that verifies credentials and supplies the
role. See [authentication handlers](../authentication/index.md).

## What a sample contains

`arc.QueryHealth` serializes as:

| Member | Meaning |
| --- | --- |
| `observations` | One `queries.ObservationHealth` per registered query label (or `_other`) with `opening`, `running`, `closing`, `retained` and `delivered` counts |
| `hubs` | One entry each for `sse` and `websocket` with `connected` and `closing` physical connections |
| `openings` | Hub subscriptions still in admission or source opening |
| `operations` | Active plus retired-but-unjoined hub subscriptions |

`retained` counts observations whose cleanup has not confirmed a join, which is the
number to watch for leaked sources. `delivered` counts acknowledged data results
for the currently owned observations, not historical traffic.

A sample never contains connection or query IDs, principals, client details,
arguments, cached results or exception messages. Application observations exclude
the health query itself. Hub operations and observations overlap, so do not add
them together. Each owner is sampled under its own lock; the sample as a whole is
not globally atomic.

## Isolation from your application

The health query runs in a private pipeline. It never activates your request
readers, resource openers, scope factories, filters, validators or policies, and
it rechecks the role on every emission. Streaming uses the consumer's own
one-second timer, so no background publisher runs. Health observations count
against a separate ceiling equal to `Observable.MaxObservations`.

For per-artifact counts and durations, add [backend diagnostics](../core/diagnostics.md);
the health pipeline records into the same recorder.

## Evidence

`query_health_test.go` and `ExampleQueryHealth` cover absence without opt-in,
role validation and copying, private-pipeline isolation from application
factories and readers, one-second sampling that excludes itself, hub role
enforcement with aggregate-only data, private no-store streaming and QUERY route
gating. See the [parity map](../../../parity.md) for the deliberate differences
from C#.
