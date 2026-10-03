---
title: Observable query hub contract
description: Multiplex query subscriptions with revision ownership, bounded delivery and SSE control ownership.
---

Use a hub to carry several subscriptions over one physical connection. Both hubs
support full, delta and legacy collection transfer. The JavaScript runtime's
default is one WebSocket hub with delta transfer; Go implements that wire path,
but real locked `@cratis/arc` and browser execution remain unverified.

## Routes and opening

| Method | Route | Purpose |
| --- | --- | --- |
| GET | `/.cratis/queries/ws` | WebSocket hub upgrade |
| GET | `/.cratis/queries/sse` | SSE hub stream |
| POST | `/.cratis/queries/sse/subscribe` | SSE subscribe control |
| POST | `/.cratis/queries/sse/unsubscribe` | SSE unsubscribe control |

These framework routes do not depend on API prefixes or discovery exposure.
HEAD on either hub is a nonstreaming probe that allocates no connection.
Connected is written before activating subscription data:

```json
{"type":"Connected","payload":"opaque-connection-id","keepAliveIntervalMs":30000,"supportsSubscriptionRevisions":true}
```

The ID is generated per physical connection, not an authentication credential.
Hub SSE wraps each message in `data: <JSON>\n\n`. Keepalives are JSON Ping hub
messages, not SSE comments; there is no SSE ping endpoint or Pong control.
WebSocket application Ping/Pong uses Unix milliseconds and echoes timestamps.

## Subscribe and unsubscribe

WebSocket control uses `payload`. This example assumes the registered query's
fully qualified identity is `Task.All`:

```json
{"type":"Subscribe","queryId":"tasks","revision":1,"payload":{"queryName":"Task.All","arguments":{},"transferMode":"delta"}}
```

SSE uses `connectionId` and `request`, not the WebSocket payload envelope:

```json
{"connectionId":"opaque-connection-id","queryId":"tasks","revision":1,"request":{"queryName":"Task.All","arguments":{},"transferMode":"delta"}}
```

Arguments are strings or null, not arbitrary JSON QUERY arguments. Paging/sorting
uses flat `page`, `pageSize`, `sortBy` and `sortDirection` fields. Hub sorting
requires both sorting fields; direction `desc` is case-insensitive, otherwise
ascending. Transfer mode is case-insensitive; missing or unknown text selects
legacy, not delta.

Unsubscribe uses the **current** revision, not necessarily a newer one:

```json
{"type":"Unsubscribe","queryId":"tasks","revision":1}
```

```json
{"connectionId":"opaque-connection-id","queryId":"tasks","revision":1}
```

Supplied revisions must be lexical integers from 1 through 9007199254740991.
Strings, exponent notation, fractions, zero and overflow fail. Absent/null means
legacy. A newer subscribe replaces its owner; duplicate/older revisions do
nothing. Equal/newer unsubscribe cancels and keeps a tombstone, even before any
subscribe. Once an ID is revision-aware, legacy controls cannot override it.

Go retains bounded high-water marks for the **connection lifetime**, unlike C#'s
expiring tombstones. IDs are not silently evicted at capacity. Reconnect creates
fresh revision state and a full baseline; it does not resume.

## Results and failures

QueryResult messages have `type`, `queryId`, optional `revision` and `payload`
containing the complete QueryResult. Unauthorized has no payload. Error contains
a safe string payload. Output type names use exact PascalCase; absent optional
fields are omitted rather than null. One subscription's denial/error does not
terminate its siblings. No later frame may follow that owner's terminal frame.

SSE controls return empty bodies, including failures:

| Condition | Status |
| --- | --- |
| Accepted, duplicate, stale or compatible no-op | 200 |
| Invalid required fields or malformed JSON | 400 |
| Unknown/wrong-owner connection | 404 |
| Initial subscription denial, also sending Unauthorized | 401 |
| Body too large | 413 |
| Capacity admission rejected | 429 |
| Application stopping | 503 |

Unknown queries/source failures send safe hub Error messages. WebSocket malformed
or binary controls close the physical connection rather than creating a command
result. Go rejects case-insensitive duplicate JSON members, including Unicode
simple-fold equivalents.

## Own SSE controls securely

SSE controls must match the GET's verified subject, authentication state and tenant
(including tenant presence). Wrong ownership returns 404 without exposing the
connection. WebSocket subscriptions use the handshake identity/tenant; arbitrary
socket data cannot refresh credentials. Subscription metadata/resources have their
own lifetime and do not borrow the short-lived POST request scope.

Anonymous SSE uses a different random HttpOnly cookie for each connection,
SameSite=Strict, Secure on TLS, no Domain. It covers the hub and its controls and
expires with the bounded connection lifetime. Keep a cookie jar for terminal use:

```bash
curl -N -c cookies.txt 'http://localhost:8080/.cratis/queries/sse'
```

After reading Connected, use another terminal, replace the connection ID and query
identity, and retain the same cookie jar:

```bash
curl -i -b cookies.txt -H 'Content-Type: application/json' \
  -d '{"connectionId":"opaque-connection-id","queryId":"tasks","revision":1,"request":{"queryName":"Task.All","arguments":{},"transferMode":"delta"}}' \
  'http://localhost:8080/.cratis/queries/sse/subscribe'
```

A 200 response has an empty body; the result appears on the GET stream. The cookie
is ownership evidence, not user authentication. `Observable.AnonymousOwner` can
supply verified host/session evidence instead. Parallel connections use different
cookie names; eligible later hub responses expire closed cookies.

Same-origin policy uses actual TLS and Host, never untrusted forwarded headers.
`Observable.AllowedOrigins` adds exact origins, not wildcard credentials or CORS
middleware. `Origin: null` requires explicit opt-in. Cross-origin use also needs
host-owned credentialed CORS/session configuration; built-in Strict cookies target
the same-origin path. Browser WebSocket/EventSource cannot generally add arbitrary
credential headers; provide a compatible host authentication mechanism.

## Limits and shutdown

Zero observable option fields select these defaults:

| `ObservableOptions` field | Default |
| --- | --- |
| `MaxObservations` | 1024 application operations, including opening/unjoined |
| `MaxConnections` / `MaxConnectionsPerOwner` | 256 / 8 hub connections |
| `MaxSubscriptions` / `MaxQueryIDs` | 64 outstanding operations / 1024 IDs per connection |
| `MaxOpenings` / `MaxOpeningsPerConnection` | 32 / 4 |
| `MaxOutboundJobs` | 64 queued/in-flight jobs per connection |
| `MaxQueuedBytes` / `MaxStreamingBytes` | 32 MiB connection / 256 MiB application retained hub bytes |
| `WriteTimeout` / `CloseGrace` | 10 seconds / 5 seconds |
| `KeepAliveInterval` / `ConnectionLifetime` | 30 seconds / 12 hours |
| `MaximumWait` | 5 minutes |

`HTTP.MaxBodyBytes` defaults to 1 MiB for controls; `HTTP.MaxResponseBytes` defaults
to 16 MiB for encoded frames and each hub baseline. Query IDs are at most 256
bytes, query names 1024 bytes, arguments 128 entries. Frame/baseline candidates
and delivered baselines share connection/application byte budgets.

Queue/byte exhaustion closes the slow connection; an oversized emission terminates
its subscription with safe diagnostics. Opening/retired workers count until they
join, so replacement cannot bypass limits. Limits cannot bound application callback
allocations. There is no silent delta loss or drop-oldest queue policy.

One writer serializes frames and keepalives. Queue admission is not delivery;
only successful local write/flush advances the [collection baseline](change-stream.md).
An already-started write cannot be retracted by replacement; revision-aware clients
must discard obsolete revisions. Legacy replacement fences earlier writes.

Application shutdown cancels and joins hub readers, writers and subscriptions,
including hijacked sockets, before user hooks. Hub WebSocket shutdown is a bounded
disconnect, not a promised graceful close frame. A failed join leaves Stopping and
can be continued with a later Shutdown.
