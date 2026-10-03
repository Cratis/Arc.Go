---
title: Mount Arc in Gin
description: Adapt Arc's standard handler without losing custom QUERY or discovery routes.
---

## Forward unmatched Arc requests

For an existing Gin host, use Gin's standard-library handler adapter in a fallback
which does not compete with your Arc paths. This excerpt assumes an initialized
Gin engine `engine`, the Gin import `gin`, and started Arc application `app`:

```go
engine.NoRoute(gin.WrapH(app))
```

Do not enable a Gin method-not-allowed handler which intercepts Arc QUERY requests
before this fallback. Reserve `/.cratis/*` and operation paths, and keep the original
URL unchanged. Arc owns its empty 404/405 behavior inside its handler. This recipe
adds no Gin dependency to Arc itself; compilation belongs to your Gin application.

An authenticated Gin user becomes Arc authority only through explicitly installed
`identity.WithPrincipal` metadata and a registered `authentication.HostPrincipal()`
adapter. Framework-local values or display identity cookies are not proof.

Gin owns its external server/listener/timeouts. Coordinate graceful server shutdown
with `app.Shutdown` under a fresh cleanup budget. See [hosting](../core/hosting.md).
