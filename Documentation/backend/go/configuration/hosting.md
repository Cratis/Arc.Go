---
title: Hosting options
description: Reference Arc Go hosting configuration defaults, request limits and server timeout budgets, including option validation and borrowed collaborator rules.
---

Pass `arc.Options` to `NewBuilder` to configure an Arc application. The builder
copies and validates the options before activation or I/O. Negative limits and
timeout values fail; zero selects defaults for those limits and timeouts.
Configuration slices, pointed-to configuration values and role lists are copied,
but collaborator values and callbacks remain borrowed. They must support concurrent
use; copying the options does not make those collaborators private to the
application. Set configuration values before creating the builder.

Choose who owns the HTTP server before changing its timeouts. Arc applies the
owned-server settings when you use `Run` or `Serve`; when you embed the application
in your own server, you configure that server. The [hosting guide](../core/hosting.md)
covers the lifecycle choices; this page is the field reference for those choices.

## Defaults

Leave `Routes` nil to select `metadata.DefaultOptions()`. If you supply a pointer,
Arc uses those route options as written: an empty prefix, false name flags and
disabled QUERY are retained rather than filled from the defaults. To change only
one default, start with `metadata.DefaultOptions()` and change that field before
passing its address. See [route options](routing.md) to relate those fields to a
resolved command or query path.

| Option | Default or contract |
| --- | --- |
| Environment | Production; only explicit Development opens discovery by default |
| Namespace | Empty logical namespace; never inferred from a Go import path |
| Routes | `metadata.DefaultOptions()` when nil; nonnil zero values retain meaning |
| Authentication | No handlers; typed upstream principal requires HostPrincipal |
| Tenancy | Header `x-cratis-tenant-id`; custom resolver and configured selector are exclusive |
| Membership / RequireTenant | Off; selection does not grant membership |
| OpenResources / ScopeFactory | Optional and mutually exclusive; resources own operation disposal |
| DependencyCatalog | Optional static declared-key checks, never dependency resolution |
| Clock | `time.Now`; receipts are captured before HTTP authentication |
| CleanupTimeout | 30 seconds, cooperative |
| ExposeExceptionDetails | False in every environment |
| Logger | Nil, silent |
| Identity.DetailsProvider | Select sole custom provider or authorized empty-details default |

## HTTPOptions

Use the limits below to bound request input and encoded response publication.
The server timeouts and `MaxHeaderBytes` apply to the `http.Server` Arc owns through
`Run` or `Serve`; they do not configure a server in which you embed Arc. Configure
those server settings on your own host when embedding.

| Field | Zero/default |
| --- | --- |
| MaxBodyBytes | 1 MiB |
| MaxQueryBytes | 8 KiB raw query string |
| MaxResponseBytes | 16 MiB encoded publication |
| CorrelationHeader | X-Correlation-ID; valid nonconflicting token required |
| ReadHeaderTimeout | 5 seconds |
| ReadTimeout / WriteTimeout | 30 seconds |
| IdleTimeout | 60 seconds |
| MaxHeaderBytes | 1 MiB |
| ShutdownTimeout | 30 seconds |
| QueryReaders | Standard GET and QUERY readers; exact-method replacements allowed once |

A response limit bounds publication after encoding, not every allocation inside a
trusted custom codec. Set it with that distinction in mind; it is not a memory
budget for codec execution.

Read any process environment or configuration files in your application, then
supply the resulting values through `arc.Options`. Arc does not run a
process-environment lookup or general-purpose configuration loader. The
[hosting example](../../../../examples/hosting/README.md) shows the surrounding
host setup. For which discovery endpoints to expose, use
[discovery options](../introspection/index.md).
