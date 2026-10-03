---
title: Hosting options
description: Look up construction defaults, unary HTTP limits and owned server budgets.
---

`arc.Options` is copied during `NewBuilder`. Collaborator values and callbacks
are borrowed and must support concurrent use; slices, pointed-to configuration
values and role lists are copied. Negative limits/timeouts fail construction.

## Defaults

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

Owned-server timeouts do not configure embedded servers. Response limits bound
publication after encoding, not every allocation inside a trusted custom codec.
No process-environment lookup or general-purpose configuration loader runs inside
Arc. See [discovery options](../introspection/index.md) for exposure settings.
