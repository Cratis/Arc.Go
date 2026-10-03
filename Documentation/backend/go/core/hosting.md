---
title: Host commands and queries
description: Choose owned HTTP serving or embed an Arc application as an http.Handler.
---

Arc keeps business execution in the command/query pipelines and transport in the
application. You can own a small server or mount the same application in an
existing host; neither requires a container or Chronicle connection.

## Build once

Create `arc.NewBuilder(arc.Options{})`, register typed commands/read-model queries,
and call `Build`. Registries are borrowed construction collaborators; do not call
their `Build` methods independently. The root Build attempt seals registration,
including on failure. A successful repeated Build returns the same application;
a failed attempt returns its recorded error. Create a new builder to correct it.

`Catalog` and `Endpoints` return copied snapshots. The latter includes explicit
HEAD and mapped framework endpoints. Raw handlers are not fabricated into catalogs.
Manual and generated registrations use the same registrar interfaces; the
composition generator itself is not delivered by hosting.

## Own a server

`app.Run(ctx, "127.0.0.1:8080")` owns listening and serving. `app.Serve(ctx, listener)`
takes ownership of an existing listener, including closing it on startup failure.
Only one owned server is accepted. Neither API installs signal handlers.
Cancellation initiates shutdown with a fresh bounded context and joins the server
worker before returning. Unexpected serve/start/cleanup errors remain inspectable.

## Embed a handler

Call `app.Start(ctx)` before serving, then mount `app` as an `http.Handler` without
rewriting Arc paths. The external host owns its listener, server timeouts and
server shutdown. Coordinate that shutdown with `app.Shutdown(cleanupCtx)` using a
fresh cleanup budget. Unstarted/stopping applications return empty 503.

Use `builder.Handle(pattern, handler)` for small raw endpoints. Patterns use
ServeMux syntax. Catch-all and subtree handlers can overlap Arc paths, so you can
serve a SPA fallback. Arc's exact routes retain priority, including their 405
responses. Arc always owns `/.cratis/*`; an unmapped reserved path returns empty
404, never the fallback. Exact ownership conflicts fail Build, including
host-constrained patterns. Raw handlers receive resolved ingress metadata and
are not implicitly wrapped in result envelopes.

## Boundaries

POST and `/validate`, GET/HEAD and QUERY use the existing pipelines. HEAD executes
the GET read flow but suppresses bytes; it is not a free metadata probe. Snapshot
queries stay snapshots even with an SSE Accept header. Hubs, waits, OpenAPI and
browser-client conformance remain unsupported.

After admission, Arc recovers middleware and raw-handler panics. Before response
commitment it returns a redacted command/query 500, or empty 500 for raw,
identity and discovery endpoints. After commitment or hijacking it logs the fault
without appending an error body. `http.ErrAbortHandler` is re-panicked so the
HTTP server retains its abort semantics. Arc's owned server routes its ErrorLog
through the configured `slog.Logger`; a nil logger stays silent. Embedded hosts
must configure their own server logger.

See [HTTP contract](../reference/http-contract.md)
and [lifecycle](lifecycle.md) for error and ownership behavior.
