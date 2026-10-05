---
title: Run observable MongoDB queries on several instances
description: Route hub traffic with owner affinity, size limits per instance, and shut instances down without leaking change-stream cursors.
---

Once you run more than one copy of an application behind a load balancer, an
observable query is no longer a single process concern. Each instance must notice
database changes on its own, a client's control requests must reach the instance
that holds its stream, and losing an instance must not leave cursors or resources
behind. This page describes how Arc.Go behaves there and what you must provide.

Status: **Partial**. The evidence is two independent Arc applications behind a
real loopback HTTP router, sharing one MongoDB 8.0.15 replica set. It is not a
multi-process, browser or production load-balancer certification.

## Each instance is independent

Every application instance owns its own MongoDB client, `mongodb.Watcher`, query
pipeline and hub registry. Nothing is shared in memory, and Arc.Go has no broker
or distributed subscription registry. Only the database and collection
coordinates are common.

- Each instance opens its own change-stream cursor for each tenant database it
  observes. Two instances watching two tenant databases hold four cursors.
- A database change reaches every instance through its own cursor. Each
  subscriber then rerenders with its own authorized row filter, so another
  principal's or tenant's rows are never delivered as a side effect.
- Multiple principals on one instance share that instance's database cursor, not
  rows.

You do not need to coordinate instances for correctness of delivered data. You do
need routing affinity for controls.

## Route hub controls with owner affinity

A Server-Sent Events hub connection lives on the instance that accepted its
`GET /.cratis/queries/sse`. The subscribe and unsubscribe controls are separate
`POST /.cratis/queries/sse/subscribe` and `/unsubscribe` requests that name the
connection ID. They only work on that same instance.

| Control request | Result |
| --- | --- |
| Correct owner, same instance | 200; the result arrives on the stream |
| Correct owner, **other** instance | 404; no resources are opened |
| Wrong subject or tenant, an anonymous caller controlling an authenticated owner's connection, or an anonymous caller whose ownership evidence (connection cookie or `AnonymousOwner` result) does not match, on the right instance | 404 |
| Any caller after the owning instance is gone | 404 |

Configure your load balancer so that the hub GET and its controls reach the same
instance, for example with load-balancer session affinity for the client. The
ownership check still applies on top of routing: affinity is required, but it does
not grant access. See [own SSE controls securely](../queries/observable-query-hub.md#own-sse-controls-securely)
for the ownership rules.

Direct observable SSE requests carry the query in the GET itself and need no
affinity beyond the lifetime of that one response. The WebSocket hub sends
controls over the same socket, so they cannot reach another instance; the
two-instance tests exercise the SSE hub and direct SSE, not the WebSocket hub.

## Size limits per instance

All limits apply to one application instance, not to the deployment:

- `ObservableOptions` limits such as `MaxObservations`, `MaxConnections`,
  `MaxConnectionsPerOwner` and `MaxSubscriptions` (see the
  [hub limits](../queries/observable-query-hub.md#limits-and-shutdown)).
- `WatcherOptions` limits such as `MaxDatabases`, `MaxSubscribers`,
  `MaxSubscribersPerQuery` and `Buffer`.

A caller allowed eight hub connections per owner can therefore hold eight on each
instance. Size database change-stream capacity for instances times observed tenant
databases.

## Lose an instance

When an instance's connections drop, its streams end. That instance's watcher
cursors and invocation resources are joined when you shut it down. Controls naming
its connections return 404 on the surviving instances. Survivors keep delivering
to their own subscribers.

Recovery is explicit and has no continuation token. The client opens a new hub
connection, receives a new connection ID and subscribes again. The first result is
a full baseline even when the query ID and revision are reused. An unsubscribe and
resubscribe on the same connection also resets that subscription's delta baseline.
There is no transparent resume, connection migration, replay or gap-free delivery
across the loss.

## Shut an instance down

Shut down in ownership order:

1. `Application.Shutdown` cancels and joins observations, hub subscriptions and
   their invocation resources.
2. `Watcher.Close` joins the database readers and closes their change-stream
   cursors.
3. Disconnect your borrowed MongoDB client.

The two-instance tests check that every opened invocation resource is closed
exactly once, that each instance issues one `killCursors` per observed database,
and that the borrowed client is still usable after the watcher closes. See
[MongoDB observation](index.md#observe-an-authorized-collection) for the watcher
lifecycle on one instance.

## Evidence and limits

`integrations/mongodb/two_instance_integration_test.go` runs in the tagged Linux
MongoDB lane:

- `TestLiveTwoInstanceProviderBroadcastAndIsolation` covers broadcast to both
  instances, tenant and principal isolation, and per-instance cursor counts.
- `TestLiveTwoInstanceHubAffinityLossAndExplicitBaselineReset` covers wrong-instance
  and wrong-owner control rejection without opening resources, transport loss,
  explicit reconnect, full baselines and the unsubscribe/resubscribe reset.

Not covered: separate OS processes, process crashes, a real load balancer or
browser, MongoDB failover or sharding, and the WebSocket hub across instances.
