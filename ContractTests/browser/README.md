# Real-browser observable contracts

This lane runs the locked Cratis frontend packages in headless Chromium against
the real Arc.Go host. The Node lane in
[observables](../observables/README.md) cannot cover browser `EventSource`,
cookies or React `StrictMode`, so this lane does.

## Running it

From the repository root:

```bash
GOWORK=off GOTOOLCHAIN=local node ContractTests/browser/run.mjs
```

It requires Node **26.8.1**, npm **12.0.2**, local Go **1.26+**, and the
Chromium revision selected by the locked `playwright-core` **1.63.0**. The runner
never downloads a browser. CI installs only the headless shell with
`npx --no-install playwright-core install --with-deps --only-shell chromium`
from this directory. Locally, run the same command or reuse a matching
`ms-playwright` cache. A missing browser fails the `runtime` stage; nothing is
skipped.

To run one stage, append `install`, `bundle`, `host-build` or `runtime`. Later
stages need the earlier stages' outputs:

- `install` runs `npm ci --engine-strict`.
- `bundle` builds `app.jsx` with esbuild **0.25.10** into
  `.ai-work/browser-contract/assets`, using development React.
- `host-build` builds `./ContractTests/browser/host`.
- `runtime` runs `browser.test.mjs` with a 60-second bound per case and
  240 seconds for the stage.

The dependencies are exactly the pins in the Node lane: `@cratis/arc` and
`@cratis/arc.react` **22.48.2**, `@cratis/fundamentals` **7.22.0**, and React and
React DOM **18.3.1**. Their integrity values match the Node lane. The bundle
resolves every Cratis and React import from this package, so the page loads one
copy of each.

## The application under test

`app.jsx` mounts the real exported `<Arc>` wrapper around a component that calls
the production-generated `All.use` hook from
`../observables/frontend/Generated`. Only the URL selects options:
`transport=ws` passes `QueryTransportMethod.WebSocket`, `strict=1` wraps the tree
in `StrictMode`, and `retention` sets `queryCacheRetentionMs`. Without
`transport`, the wrapper's own default applies, which is the SSE hub. No test
reducer, transport shim or handwritten query class is involved.

The host is `clientfixture.NewBrowser` served by `clientfixture.ServeBrowser`. It
uses Arc's default cookie-owned anonymous hub sessions and the same-origin
policy, and serves the bundle at `/fixture/browser/` on the same origin.
`POST /fixture/shutdown` performs a real server close: the host joins the current
Arc generation, checks that every opened source closed, and serves a fresh
fixture on the same address. `SIGTERM` makes the host join and print a final
report, with opens equal to closes.

## Cases

Each case starts its own host and browser context, and ends by stopping the host
and checking its joined report.

- **Default `<Arc>` SSE hub with cookie ownership.** One `EventSource` and no
  WebSocket. The hub cookie is `HttpOnly`, `SameSite=Strict`, scoped to
  `/.cratis/queries/sse`, not `Secure` on plain-HTTP loopback, and named after
  the connection. Its value differs from the connection ID, and
  `document.cookie` cannot read it. The browser attaches it to the subscribe
  POST. Delta updates render the reconstructed collection with hydrated dates.
  A second browser profile that knows the connection and query IDs is refused
  with 404 for subscribe and unsubscribe. Neither refusal changes lifecycle
  counters, and the owner keeps receiving updates.
- **WebSocket hub.** One socket on `/.cratis/queries/ws` carries
  `Connected`, `Subscribe` and `QueryResult`. No `EventSource` opens and no hub
  cookie is set. Updates render, and the server has exactly one live source.
- **`StrictMode` double mount**, with default cache retention and with immediate
  eviction. After the development double mount there is one hub connection and
  exactly one live server source, and updates render once. With immediate
  eviction, a real unmount joins the source, and every subscribe POST opened
  exactly one source.
- **Reconnect after a real server close.** The page gets no hint. The
  `EventSource` error drives the pinned `ReconnectPolicy`, a new hub connection
  receives a new connection ID and `HttpOnly` owner, and the subscription is
  re-sent. The fresh generation's baseline replaces the update from before the
  close, and later updates render.
- **Terminal `Unauthorized`.** A denying emission guard makes the hook settle as
  ready and unauthorized. The denied value never renders, the server joins the
  source, and the client never subscribes again: one subscribe POST and one
  opened source for the whole host lifetime.
- **Joined shutdown.** An SSE page and a WebSocket page each hold one live
  source. `SIGTERM` makes the host join with both sources closed and every
  resolved resource disposed. Both pages then observe the close: an
  `EventSource` error and a WebSocket `close`.

Failures keep console output, requests, WebSocket frames, render projections
and host reports in the ignored `.ai-work/keep/browser-observable-failure.json`.
CI uploads that file only on failure. It is diagnostic evidence, not a golden
capture.

## Coverage boundaries

This lane uses Chromium on Linux only, over plain-HTTP loopback. It does not
cover Firefox, WebKit, TLS `Secure` cookies, cross-origin or credentialed CORS,
authenticated principals, HTTP/2 multiplexing limits, suspense, paging hooks or
direct per-query transports. The reconnect case covers the pinned client's
first back-off, not long outages or `ReconnectPolicy` exhaustion.
