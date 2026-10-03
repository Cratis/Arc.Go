# Observable transport contracts

Run `GOWORK=off GOTOOLCHAIN=local go test -count=1 -timeout=2m ./ContractTests/observables`
from the repository root. The race lane runs this same harness with `-race`.

The harness starts real loopback listeners on ephemeral ports, uses the public
Arc builder and pipeline, and shuts down active SSE and hijacked WebSocket
connections before closing listeners. Each read and control has a bounded
lifetime; there are no sleeps or polling loops.

Independent expectations use the C#/JavaScript contract at
`7c1e78075b737df64f69fddfaae83374f75e3612`, documented in the
[observable fixture provenance](../fixtures/v1/observable/provenance.md).
Tests cover:

- Direct SSE complete frames and direct WebSocket Data envelopes, full successive
  snapshots, stable validated correlation IDs, and final unauthorized results.
- Both hubs' Connected ordering, Full/Delta/Legacy results, ordered added/replaced/
  removed items, required empty arrays, and omitted versus present data/change sets.
- Current-revision unsubscribe, stale/legacy resurrection rejection, unsubscribe
  before subscribe, fresh replacement baselines and non-resumable reconnect.
- Terminal hub Unauthorized without affecting another subscription.
- Joined application shutdown with all four transports active, including repeated
  shutdown. WebSocket hub shutdown is a bounded disconnect, not a claimed orderly
  close handshake.

The WebSocket client is a small independent masked RFC fixture reader/writer,
not the server's codec and not a production client. Root transport tests separately
cover HTTP/2, fragmented input, application Ping/Pong, origin/owner rejection,
capacity and deadlines. In-memory tests cover failed write acknowledgements,
legacy fences, baseline privacy and truthful uncooperative cleanup.

## Locked JavaScript client gap

The locked `ContractTests/ProxyComparison` harness is now present, including
`package.json` and `package-lock.json`. Its captured C# proxy checks do not exercise
the observable runtime. No observable JavaScript client lane has been run.

Real `@cratis/arc` default WebSocket/delta execution, Guid/model hydration,
React change-stream reconstruction, browser EventSource cookies, unsubscribe and
reconnect behavior remain **unverified**. Raw Go wire tests are not browser
compatibility evidence. Reuse the locked harness read-only and add observable
client cases in this directory; do not adapt upstream clients
to accommodate the Go implementation.
