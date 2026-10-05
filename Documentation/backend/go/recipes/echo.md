---
title: Mount Arc in Echo
description: Delegate every unmatched method and path from Echo v5 to Arc's http.Handler.
---

Your service already uses Echo and you want Arc's commands and queries beside
your Echo routes. Echo v5's `Any` route matches every method, including QUERY,
so one wildcard route hands Arc everything your own routes do not claim.

## Delegate to Arc

Build and start Arc first. This code is compiled and tested in the
[recipes module](index.md):

```go
// NewEcho returns an Echo instance that serves host routes and forwards every
// other request, unchanged, to the started Arc application.
func NewEcho(app http.Handler) *echo.Echo {
    e := echo.New()
    e.GET("/healthz", func(c *echo.Context) error {
        return c.NoContent(http.StatusNoContent)
    })
    // Echo v5's Any matches every method, including QUERY.
    e.Any("/*", echo.WrapHandler(app))
    return e
}
```

Method-specific Echo routes, such as `GET /healthz`, take precedence over the
`Any` route. Echo passes the original request to Arc, so Arc's routes, empty
404/405 responses and `Allow` headers reach the client unchanged.

:::caution[Echo v4 is different]
The recipe targets Echo v5 (`github.com/labstack/echo/v5`). Echo v4's `Any`
registers a fixed method list; check whether your version includes QUERY and
register it explicitly with `Add` if not. The recipe module does not test v4.
:::

## What the recipe proves

The test serves the instance over real HTTP with Echo v5.4.0 and checks command
execution, `/validate` without execution, GET and QUERY argument binding, HEAD,
Arc's empty 404/405, and that cancelling the client request cancels the query's
context. Direct SSE flushes two ordered results, direct WebSocket upgrades and
sends two results, and disconnect and application shutdown join the source.
Correlation IDs and QUERY's `no-store` survive the host. It does not cover other
Echo versions, hub multiplexing or Echo middleware you add.

## Identity and lifecycle

Use an Arc authentication handler such as the [JWT bearer recipe](jwt.md), or
`authentication.HostPrincipal()` after your host has installed a verified Arc
principal. Your host owns the listener and timeouts; shut the server down
first, then call `app.Shutdown` with a fresh bounded context. See
[hosting](../core/hosting.md) and the [HTTP contract](../reference/http-contract.md).
