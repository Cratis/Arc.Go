---
title: Trace requests with OpenTelemetry
description: Wrap Arc with otelhttp so each request gets a server span and handlers join the caller's trace.
---

You run Arc behind other services and want one trace across them: the caller's
`traceparent` should become the parent of a server span, and your command and
query handlers should log and call downstream services inside that span. Arc
has no OpenTelemetry dependency; wrap the application with
[otelhttp](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp).

## Wrap the application

This code is compiled and tested in the [recipes module](index.md):

```go
// Instrument returns app wrapped in one OpenTelemetry server span per request.
// The incoming trace context becomes the parent, and Arc handlers receive the
// server span through their request context.
func Instrument(app *arc.Application, provider trace.TracerProvider, propagator propagation.TextMapPropagator) http.Handler {
    // Name spans after Arc's own endpoints. Unmapped paths fall back to the
    // method alone so a caller cannot create unbounded span names.
    routes := make(map[string]bool)
    for _, endpoint := range app.Endpoints() {
        routes[endpoint.Method+" "+endpoint.Path] = true
    }
    return otelhttp.NewHandler(app, "arc",
        otelhttp.WithTracerProvider(provider),
        otelhttp.WithPropagators(propagator),
        otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
            if name := r.Method + " " + r.URL.Path; routes[name] {
                return name
            }
            switch r.Method {
            case http.MethodGet, http.MethodHead, http.MethodPost, "QUERY":
                return r.Method
            }
            return "HTTP"
        }),
    )
}
```

Pass your SDK's tracer provider and `propagation.TraceContext{}` (or a
composite propagator). Passing them explicitly keeps the recipe independent of
the global OpenTelemetry state. Inside a handler,
`trace.SpanContextFromContext(ctx)` returns the server span.

## Why a span name formatter

otelhttp's default names a span after the request method and the
`http.Request.Pattern` of a standard-library route. Arc does its own routing, so
the default would name every span after the method only, and it names QUERY
requests `HTTP` because semantic conventions only know the RFC 9110 methods.
The formatter names a span after the Arc endpoint, such as
`QUERY /api/tasks/search`. Unknown paths keep a bounded name.

The attributes still follow the semantic conventions: a QUERY span records
`http.request.method` as `_OTHER` and `http.request.method_original` as
`QUERY`. Query your tracing backend on the original method.

## What the recipe proves

With otelhttp v0.72.0 and the OpenTelemetry SDK v1.47.0, the tests check the
parent span from an incoming `traceparent`, that the query handler observes the
server span, the QUERY span name and method attributes, bounded names for
unmapped paths, and the full mount contract through the wrapper. Metrics and
exporters are outside the recipe; configure them in your SDK setup.
