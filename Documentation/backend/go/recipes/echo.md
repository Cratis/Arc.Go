---
title: Mount Arc in Echo
description: Delegate exact operation and discovery paths to Arc's http.Handler.
---

## Forward the original request

An existing Echo application can adapt Arc through Echo's standard handler bridge.
This excerpt assumes an initialized engine `engine`, the Echo import `echo`, and
started Arc application `app`:

```go
engine.Any("/*", echo.WrapHandler(app))
```

Verify your installed Echo version's Any method list includes QUERY. If it does
not, register QUERY explicitly using its method-registration API with the same
adapter. Reserve Arc operation paths and `/.cratis/*`, do not rewrite the prefix,
and test GET, HEAD, POST and QUERY through the complete outer host. Echo is not an
Arc runtime dependency; this recipe does not claim an adapter-version test matrix.

Use `authentication.HostPrincipal()` only after your host has installed a verified
typed Arc principal. The external host owns server/listener/timeouts and must
coordinate its shutdown with `app.Shutdown` using a fresh cleanup budget.
See [embedding](../core/hosting.md) and [HTTP methods](../reference/http-contract.md).
