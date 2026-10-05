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

## Serve an embedded public site

To serve a small site beside your queries, keep the files and fallback policy in
an ordinary `http.Handler`, registered through `builder.Handle("/", handler)`.
The [compiled static-site example](../../../../static_files_example_test.go)
embeds three files and supplies an example-local `newStaticSite` helper; it is
not a framework API. Run it from the repository root:

```bash
go test -run '^ExampleBuilder_Handle_staticFiles$' -v .
```

It prints 200 for `/assets/app.js`, `/app/tasks`, and `/api/site-message` through
real HTTP. These excerpts come from that example (the full source includes error
handling, the closure query, host startup, and shutdown):

```go
site, err := fs.Sub(staticSiteFiles, "testdata/staticfiles/site")
if err != nil {
    return err
}
```

`staticSiteFiles` is an `embed.FS` containing exactly `index.html`, `assets/app.js`,
and `assets/site.css` beneath that directory. After registering your query:

```go
if err := builder.Handle("/", newStaticSite(site)); err != nil {
    return err
}
```

The example calls `app.Start` explicitly, starts an external `httptest.Server`,
then shuts down and joins that server before `app.Shutdown` with a fresh bounded
cleanup context. In your host, retain the same ownership order.

### The example's URL and file contract

- GET/HEAD `/assets/<regular-file>` selects that relative file once. A missing
  asset returns 404, never the shell. HEAD returns the same media type and length
  without bytes.
- `/`, `/app`, and extensionless `/app/...` routes select only `index.html`.
  Missing index returns 404; `/app/missing.js`, unrelated paths, dotted app paths,
  and unknown `/api` paths (including mixed case) never receive the shell.
- Recognized site URLs reject POST, QUERY, OPTIONS, and DELETE with 405 and
  `Allow: GET, HEAD`, without opening files. Ineligible paths return 404.
- Exact Arc URLs win before the raw callback, even an asset-shaped query path:
  query successes, redacted failures, denials, and 405 responses stay Arc-owned.
  Mapped, unmapped, disabled, and mixed-case `/.cratis` paths never reach the site.
- The handler rejects dotfiles, directories, trailing-slash directory requests,
  and ambiguous paths. It performs no directory listing, default-file search,
  redirect, path cleaning, backslash reinterpretation, or double unescaping.
  Arc's existing canonical-path rejection runs first.
- Each selected file is opened once, statted, required to be regular and seekable,
  and closed on every opened-file path. Open/stat/preflight-seek errors produce
  generic 404 (missing), 403 (permission), or 500 responses without filesystem
  error text. `http.ServeContent` delivers content directly; this is not a
  response buffer that converts Arc errors into HTML. Later read, transfer, or
  close failures never append a shell and cannot retract a published response.
- Site-produced responses use `Cache-Control: no-store`, including failures.
  There is no public-cache or immutable-cache policy and no global header change;
  Arc-owned headers and statuses stay unchanged.

### Security and compatibility limits

This is a **public, nonpersonalized site**. Arc authentication ingress runs before
matched raw callbacks: terminal bad credentials prevent site and filesystem
access. Anonymous requests can still receive public content. Raw handlers do
**not** inherit command/query authorization declarations or fallback policies.
For a protected site, supply trusted-principal authentication and an explicit
site authorization boundary; do not treat a query policy as asset protection.

The runnable source uses immutable embedded assets. A trusted read-only ordinary
`fs.FS` must also provide prompt Open/Stat/Seek/Read/Close behavior; the `fs.FS`
interface does not provide cancellation or establish trust. `os.DirFS` and
`fs.Sub` are **not symlink sandboxes**. Writable or symlink-containing directories
and arbitrary filesystem implementations are outside this confinement claim.

This bounded composition witness is **Partial** static-file parity. The C#
reference at `7c1e78075b737df64f69fddfaae83374f75e3612` is
`Arc.Core/StaticFileExtensions.cs`, `Http/StaticFileOptions.cs`,
`Http/StaticFilesMiddleware.cs`, `Http/FallbackMiddleware.cs`, and
`Http/HttpListenerEndpointMapper.cs`, plus the static/fallback middleware specs.
C# uses GET only, configurable roots/default files, and the first root for a
broad fallback; a missing file falls through to the eventual 404. This Go example
instead admits HEAD, gates fallback narrowly, protects reserved ownership, and
chooses no-store caching. The
[static-site HTTP tests](../../../../static_files_test.go) exercise this
profile, not identical C# execution, paired C# tests, browser behavior, or broad
host/filesystem support. See [the parity map](../../../parity.md) for the wider
implementation boundary.

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
