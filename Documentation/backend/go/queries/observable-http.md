---
title: Inspect observable queries over HTTP
description: Read current values, wait for a first result, or stream full results with SSE and WebSocket.
---

You can inspect a live query without writing a frontend. These examples assume a
running application at `http://localhost:8080` with the observable task feed
registered at `/tasks/all` in [Keep a query live](observable-queries.md). Supply
your application's credentials when the query is protected. Actual browser/client
compatibility is unverified; these are the implemented transport contracts.

## Read or wait for a snapshot

```bash
curl -i 'http://localhost:8080/tasks/all'
curl -i 'http://localhost:8080/tasks/all?waitForFirstResult=true&waitForFirstResultTimeout=0.25'
```

A current value returns a ready QueryResult. A pending feed returns 202 without
inventing data. Waiting attaches atomically and returns the first candidate that
passes authorization, interception and guards; suppressed values do not satisfy
it. A ready nil value omits `data` but is still ready, unlike pending.

| Situation | Status |
| --- | --- |
| Allowed current value or first allowed emission | 200 |
| No allowed current value and no wait | 202 |
| First-result wait expires | 408 |
| Source completes before an allowed value | 500 |
| Authorization denied | 403 |
| Authentication rejects credentials | 401 |
| Enumerable-only registration without streaming | 400 |

Wait keys are case-insensitive; duplicates fail rather than selecting one.
`waitForFirstResult` accepts only Boolean `true`/`false` text, not `1`.
Timeouts use positive finite seconds, including fractions. Invalid/nonpositive
values use 30 seconds; `Observable.MaximumWait` caps the budget at five minutes
by default. Timeout text includes the applied budget:

```text
Timed out waiting 30 seconds for the first observable query result.
```

Normal completion before a value reports:

```text
Observable query completed before producing its first result.
```

Enumerable-only snapshot requests return the reference's exact 400 body:

```json
{"message":"AsyncEnumerable queries require WebSocket connection"}
```

Despite that wording, direct SSE is also available. HEAD checks authorization,
filters and request validity but never invokes the performer, opens a source,
waits or upgrades. Ordinary snapshot-query HEAD behavior is unchanged.

## Stream with direct SSE

```bash
curl -N -H 'Accept: text/event-stream' 'http://localhost:8080/tasks/all'
```

Each frame is `data:` followed by one space, one complete QueryResult JSON and two
newline bytes. There is no named event, SSE ID, revision or hub envelope. The first result
flushes immediately; a pending feed flushes headers without a synthetic result.
Every later result is a full snapshot, never a hub delta.

Responses use `text/event-stream; charset=utf-8`, `Cache-Control: no-cache` and
`X-Accel-Buffering: no`. HTTP/1.x includes `Connection: keep-alive`; HTTP/2 does
not. Direct SSE has no periodic keepalive enabled. A live denial sends one final
unauthorized result then ends the stream. Source/encoding failures can send a
safe terminal error; completion simply closes without an invented Completed event.

QUERY can negotiate SSE too. URL wait controls remain separate from its argument
body, and QUERY retains `Cache-Control: no-store`. Proxies must preserve flushing
and disable buffering; embedded response wrappers must support
`http.ResponseController` through `Unwrap` or the required optional interfaces.

## Use direct WebSocket

A valid WebSocket GET upgrade takes priority over SSE Accept negotiation.
Otherwise an observable GET/QUERY with SSE Accept streams; plain requests use
snapshot semantics. A snapshot-only query never turns into a live query because
of an Accept header.

Direct WebSocket sends complete text messages with exactly `type: "Data"` and
`data: <complete QueryResult>`. There is no Connected, queryId, revision or
payload envelope. Application Ping/Pong is JSON with `type` and a Unix-millisecond
`timestamp`; Pong echoes a supplied Ping timestamp. RFC ping/pong and fragmented
messages are handled by the transport library, not substituted for this protocol.
Compression is disabled.

Pre-upgrade denial remains HTTP 403. Live denial is a final Data-wrapped
unauthorized result followed by orderly close. Direct transports always use full
results; choose the [hub](observable-query-hub.md) for multiplexing and deltas.

## Disconnect and recover

A write succeeds only after local write/flush, not browser acknowledgement.
Streaming clears the absolute unary write deadline and applies
`Observable.WriteTimeout` to each write (ten seconds by default). Source cleanup
has a five-second initial grace. A timeout is not proof that cleanup joined.

Reconnect creates a new subscription and current snapshot. There is no Last-Event-ID
resume, replay cursor, durable receipt or exactly-once promise. Bound retries in
your client; do not reconnect indefinitely after a terminal Unauthorized.
