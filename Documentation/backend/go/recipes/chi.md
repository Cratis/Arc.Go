---
title: Mount Arc in Chi
description: Preserve Arc paths and QUERY dispatch when embedding in a Chi host.
---

Your service already routes with Chi and you want Arc's commands and queries
beside your own handlers. Mount the Arc application at the root so Arc keeps
its own paths, its custom QUERY method and its empty 404/405 responses.

## Mount the handler

Build and start Arc first, then hand the application to the router. This code
is compiled and tested in the [recipes module](index.md):

```go
// NewRouter returns a Chi router that serves host routes and forwards every
// other request, unchanged, to the started Arc application.
func NewRouter(app http.Handler) chi.Router {
    // Chi v5.3.1 and later route QUERY natively, so this is a no-op there.
    // Earlier versions answer QUERY with Chi's own 405 unless it is registered
    // before Mount. Registration is process-global.
    chi.RegisterMethod("QUERY")

    router := chi.NewRouter()
    router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusNoContent)
    })
    router.Mount("/", app)
    return router
}
```

`Mount` does not rewrite the request URL, so Arc sees the original path. Your
own routes, such as `/healthz`, win for their exact method and path. Every
other request reaches Arc, which answers unmapped paths with an empty 404 and a
wrong method with an empty 405 and its own `Allow` header.

:::caution[Do not add a competing catch-all]
A Chi `NotFound` or `MethodNotAllowed` handler, or another `/*` route, can take
requests before they reach Arc. Reserve Arc's operation paths and `/.cratis/*`.
:::

## What the recipe proves

The test serves the router over real HTTP with Chi v5.3.2 and checks command
execution, `/validate` without execution, GET and QUERY argument binding, HEAD,
Arc's empty 404/405, and that cancelling the client request cancels the query's
context. Direct SSE flushes two ordered results, direct WebSocket upgrades and
sends two results, and disconnect and application shutdown join the source.
Correlation IDs and QUERY's `no-store` survive the host. It does not cover other
Chi versions, hub multiplexing or Chi middleware you add.

## Own the server lifecycle

Your host owns the listener and server timeouts. Shut the server down first,
then call `app.Shutdown` with a fresh bounded context. Configure CORS and
authentication outside Arc as shown in [CORS and CSRF](cors-csrf.md) and
[JWT bearer tokens](jwt.md). See [hosting](../core/hosting.md) for the embedding
contract.
