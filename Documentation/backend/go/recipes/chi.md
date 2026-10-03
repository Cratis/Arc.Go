---
title: Mount Arc in Chi
description: Preserve Arc paths and QUERY dispatch when embedding in a Chi host.
---

## Mount the handler

In an application which already uses Chi, build and start Arc, then mount the
application's `http.Handler` without prefix rewriting. This is a host recipe,
not an Arc runtime dependency. The excerpt assumes an initialized Chi router
`router` and started Arc application `app`:

```go
router.Mount("/", app)
```

Reserve Arc's exact operation paths and `/.cratis/*`; do not install another
catch-all which steals them. Preserve the custom QUERY method through upstream
routing/proxies. Configure outer CORS explicitly if browsers need it. Install
verified principal metadata before Arc and register `authentication.HostPrincipal()`
when your Chi host authenticates credentials.

The host owns its server/listener and must coordinate server shutdown with
`app.Shutdown` under a fresh bounded context. See [embedding](../core/hosting.md).
Chi compilation is application-owned; the Arc core gates do not import Chi.
