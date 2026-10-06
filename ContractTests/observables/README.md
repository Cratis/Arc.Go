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

## Locked JavaScript client execution

From the repository root, run the single reproducible entry point:

```bash
GOWORK=off GOTOOLCHAIN=local node ContractTests/observables/frontend/run.mjs
```

It requires Node **26.8.1**, npm **12.0.2**, and local Go **1.26+**. Missing
prerequisites fail; no tests silently skip. The script runs `npm ci --engine-strict`,
generates and checks the model-bound Go adapter and TypeScript proxies through
production `arc-gen`, strictly compiles the manual and generated queries with both
decorator modes, builds
`./ContractTests/internal/observables/fixturehost`, and starts that executable on
`127.0.0.1:0`. It checks readiness through Arc HTTP admission, executes the Node
client cases, signals shutdown and awaits the host's real exit. Each producer has
a separate exit check and bound (120 seconds; runtime 60 seconds). Announcement,
readiness, requests/callbacks and shutdown have shorter bounds. To run one stage,
append `install`, `generate`, `generate-check`, `compile`, `compile-modern`,
`fixture-build`, `generated-runtime`, `react-runtime` or `runtime`; later stages
require the previous stages' outputs. Both generated runtime stages use generated
registration in separate Node processes; `runtime` keeps
the existing manual registration and eleven client tests. CI runs these stages in a dedicated frontend job, not
the native Go matrix.

The client uses exactly the ProxyComparison pins: `@cratis/arc` **22.48.2**,
`@cratis/fundamentals` **7.22.0**, TypeScript **5.9.3** and esbuild **0.25.10**.
Its separate lock also pins Node polyfills `ws` **8.22.0** and `eventsource`
**3.0.7**. Arc and Fundamentals tarball integrity values match the existing
harness. Generated hooks use the existing ProxyComparison `@cratis/arc.react` **22.48.2**,
React/React DOM **18.3.1** and their exact type pins and integrity values. Mounted
execution adds only test dependency `react-test-renderer` **18.3.1** (peer React
`^18.3.1`) and its locked transitive dependencies. No Jest, jsdom or browser
download is needed. No captured C# proxies or protocol goldens are rewritten. The authority
is Arc `7c1e78075b737df64f69fddfaae83374f75e3612`, especially JavaScript
`ObservableQueryFor.ts`, `ObservableQueryConnectionFactory.ts`, both hub
connections and `Arc.React/queries/useObservableQuery.ts`.

Executed cases in `frontend/client.test.mjs` cover:

- Default one-connection WebSocket hub/delta and explicitly selected SSE hub/delta:
  two real subscriptions, initial snapshots, real model hydration, update/add/remove
  change sets, final test-consumer collection, cancellation and source cleanup.
- Full mode on both hubs; Legacy full data plus changes through both package hub
  APIs. `Globals` exposes only Full and Delta, so Legacy uses the transport API's
  string `transferMode`, not a fictitious enum value.
- Same-ID revision-aware replacement with a fresh full baseline, package-generated
  equal-revision unsubscribe, and rejection of an exact captured Subscribe replay.
  A WebSocket Pong/ordered SSE POST acknowledgement is the negative-case barrier;
  the sibling remains live, with no retired frame, callback or reopened source.
- Both direct transports' full successive snapshots, hydration and cancellation.
  Direct modes do not expose Delta or Legacy transfers.
- Real `perform()` nil-ready versus pending; a pending WebSocket hub argument
  subscription replaced by a ready-nil argument joins its retired source.
- All four transports deliver terminal Unauthorized and join their source streams;
  direct SSE closes its EventSource. Later publishes produce no client callback.

Failures retain outgoing controls, incoming messages and callbacks in the ignored
`.ai-work/keep/observable-client-failure.json`; CI uploads that file only on
failure. This is diagnostic evidence, not a new golden capture.

## Production-generated default client case

`generatedconsumerfixture/model.go` is the authored model-bound input, not a
handwritten TypeScript query subclass. Production generation owns
`generatedconsumerfixture/zz_arc_generated.go`, `frontend/Generated/` and its
manifest. `generate-check` verifies those actual bytes without rewriting them.
The independent tools consumer test generates the same TypeScript files and
executes the fixture contracts using released Fundamentals **v0.2.0** and pushed
Arc **78ebbf8**, with `GOWORK=off`, `GOTOOLCHAIN=local`, and no `replace`.

`frontend/generated.test.mjs` exercises untouched default WebSocket hub + Delta:

- Two generated queries share a socket and hydrate rich models, including dates.
- Exact added/replaced/removed envelopes produce independent final test-consumer
  collections; revision-guaranteed baseline/delta callbacks occur once.
- Argument replacement gets a fresh baseline and a new stock-client subscription
  ID; outgoing Subscribe/Unsubscribe revisions match their actual owners.
- Independent cancellation, later sibling delivery and Ping/Pong barriers prove
  no retired callbacks. Terminal Unauthorized joins its source without revival.
- A live pending source remains host-owned until explicit shutdown; the host joins
  Arc Serve and reports cumulative opens equal closes before exiting.

The client may send a pre-negotiation Subscribe and then a revisioned replacement
on Connected. The focused case warms one genuine generated subscription first;
exact callback counts apply to the subsequent negotiated-revision cases, not an
invented once-only startup guarantee. Factory/open/close counters include that
warm-up. Go fixture tests separately reject invalid and denied generated requests
before dependency resolution or source factories, and resource holders fail if
an opened source has not joined before disposal. Both registration modes use the
same subjects, guards, controls and stream tracking.

Generated-case failures retain exact frames and callbacks in
`.ai-work/keep/generated-observable-client-failure.json`.

## Mounted generated React hooks

`frontend/react.test.mjs` mounts production-generated hooks with the real exported
`ArcContext.Provider` and `QueryInstanceCacheContext.Provider`, explicit origin,
and **explicit WebSocket hub/Delta configuration**. This is a non-StrictMode
React-in-Node renderer lane, not the complete `<Arc>` wrapper: that wrapper defaults
to SSE, whereas core `Globals` defaults to WebSocket. Generated application code
is bundled with React and Cratis imports externalized to the same locked package
instances; no handwritten hook or test reducer implements reconstruction.

The mounted cases cover:

- `All.use` and `All.useChangeStream` share one cache entry, two listeners and one
  accepted server subscription/source. They return ready/successful initial
  collections, generated `Item` instances and hydrated dates. Real replacement,
  addition and removal deltas reconstruct the hook-returned collection and rich
  change-set items; raw frames are asserted separately.
- Fresh but unchanged arguments do not reopen a subscription. Changing both hooks
  from alpha to previously unsubscribed beta receives a new baseline and retires
  alpha. The pinned change-stream hook compares beta's baseline against alpha's
  prior data rather than resetting to all-added. Retired-alpha publication followed
  by live-beta delivery and a Pong barrier bounds the no-retired-frame assertion.
- A separate `All.useWithPaging` mount returns the four-member tuple and sends
  `page: 0`/`pageSize: 1`. It reconstructs deltas, replaces arguments and joins its
  source on unmount. The fixture returns plain slices: two returned items are
  **not evidence of server-side windowing or total counts**.
- Paging, page-size and sorting setters update hook state but keep the existing
  same-key subscription in **22.48.2**. A subsequent argument replacement sends
  those updated values. This reproduces the existing open
  [Arc issue 2869](https://github.com/Cratis/Arc/issues/2869), not setter correctness;
  the test neither evicts the cache nor changes the Go host to compensate.
- Fresh `QueryInstanceCache(0)` instances retire sources asynchronously after
  unmount. Source-close and resource-dispose counters must advance before fallback
  cleanup. A separate default-retention cache retains its data/subscription with
  no listeners and reuses them on remount; explicit disposal then joins the source.
  This is **not proof that the default 30 seconds elapsed**.

A genuine warm subscription remains live for protocol barriers; factory/open/
close/dispose counters include startup and its negotiated revision replacement.
Event predicates have one absolute 3-second deadline, requests have 4 seconds,
and fixture waits have 3 seconds. No fake global clock, retry loop, sleep-based
success or render-count-as-delivery-count assertion is used. Mounted tests have a
45-second bound and their child process 60 seconds. Finally paths unmount roots,
dispose caches, cancel waiters/requests, reset the multiplexer and join socket
closure. The runner signals SIGTERM and awaits the Go host's `joined` report and
cumulative opens equal closes; a forced kill fails the lane. Failures retain frames,
hook projections, cache diagnostics, counters and errors in
`.ai-work/keep/react-observable-client-failure.json`, uploaded by CI on failure.

## Coverage boundaries

These are **Node class-runtime and bounded mounted-hook cases**, not browser or
complete React parity. No browser toolchain was added. The fixture uses an explicit
loopback anonymous session owner, not browser EventSource cookie behavior. Browser
credentials/origins, Guid hydration, DOM/StrictMode replay, suspense, network-failure
reconnect, normal source completion/error handling and broader hook/transport
combinations remain unverified by this lane.

The core Arc package forwards deltas without maintaining a final collection.
The manual and generated class tests assert actual envelopes before applying a
small independent consumer reducer; their final collections are **not** executed
React hooks. Only the separate mounted lane asserts actual hook reconstruction. Full
and Legacy assert the package-delivered full collection directly. Nil streaming
and pending argument replacement execute on the WebSocket hub only. All other
transport claims above name their executed cases; native frame tests alone do
not establish browser compatibility.
