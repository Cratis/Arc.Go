---
title: Mount Arc in Gin
description: Forward unmatched Gin requests to Arc without losing QUERY or Arc's empty 404.
---

Your service already uses Gin and you want Arc's commands and queries next to
your Gin routes. Gin has no wildcard that matches every method, so forward the
requests Gin does not route itself to Arc.

## Forward unmatched requests

Build and start Arc first. This code is compiled and tested in the
[recipes module](index.md):

```go
// NewEngine returns a Gin engine that serves host routes and forwards every
// unmatched request, unchanged, to the started Arc application.
func NewEngine(app http.Handler) *gin.Engine {
    engine := gin.New()
    engine.GET("/healthz", func(c *gin.Context) {
        c.Status(http.StatusNoContent)
    })
    engine.NoRoute(func(c *gin.Context) {
        app.ServeHTTP(immediateWriter{c.Writer}, c.Request)
        // Gin appends "404 page not found" to a NoRoute response that wrote
        // no body. Commit Arc's status so its empty 404 stays empty.
        c.Writer.WriteHeaderNow()
    })
    return engine
}

// immediateWriter restores net/http's immediate header commitment. Gin defers
// WriteHeader; Arc's wrappers hide Gin's WriteHeaderNow from WebSocket libraries,
// so an unadapted 101 would remain buffered when the connection is hijacked.
// Embedding keeps Gin's flush/hijack support and response accounting intact.
type immediateWriter struct{ gin.ResponseWriter }

func (w immediateWriter) WriteHeader(status int) {
    w.ResponseWriter.WriteHeader(status)
    w.ResponseWriter.WriteHeaderNow()
}

func (w immediateWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
```

Gin sends a request to `NoRoute` when no route matches its method and path,
including QUERY, which Gin does not register. The original URL is unchanged.

:::caution[Do not use gin.WrapH alone]
`engine.NoRoute(gin.WrapH(app))` works for most requests, but Gin writes its own
`404 page not found` body after Arc's empty 404 because Arc wrote no body. The
`WriteHeaderNow` call commits Arc's status first. The `immediateWriter` bridge
also commits status 101 before WebSocket hijacking, which otherwise leaves the
handshake buffered behind Arc's response wrappers.
:::

## What the recipe proves

The test serves the engine over real HTTP with Gin v1.12.0 and checks command
execution, `/validate` without execution, GET and QUERY argument binding, HEAD,
Arc's empty 404/405, and that cancelling the client request cancels the query's
context. Direct SSE flushes two ordered results, direct WebSocket upgrades and
sends two results, and disconnect and application shutdown join the source.
Correlation IDs and QUERY's `no-store` survive the host. It does not cover other
Gin versions, hub multiplexing or Gin middleware you add.

## Identity and lifecycle

A Gin-authenticated user is not an Arc principal. Register an Arc
authentication handler, such as the [JWT bearer recipe](jwt.md), or install
verified `identity.WithPrincipal` metadata with `authentication.HostPrincipal()`.
Your host owns the listener and server timeouts; shut the server down first,
then call `app.Shutdown` with a fresh bounded context. See
[hosting](../core/hosting.md).
