---
title: Trace requests with OpenTelemetry
description: Wrap Arc with otelhttp so each request gets a server span and handlers join the caller's trace.
---

Wrap your Arc handler with
[otelhttp](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp)
to join a caller's trace. The incoming `traceparent` becomes the parent of a
server span, and command and query handlers receive its context for logging
and downstream calls. Arc itself has no OpenTelemetry dependency; your
application owns the wrapper and provider.

## Wrap the application

Start with a built Arc application and your chosen HTTP host. Create the SDK
provider before starting Arc, then supply the wrapped handler when serving
requests. The formatter below uses registered endpoints for span names and
bounded fallbacks for other paths.

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

At shutdown, initiate HTTP draining and `app.Shutdown` before waiting for
either: active observations may need Arc cancellation before the host can
finish draining. Wait for both shutdown outcomes and check their errors.
Once the work using the provider has joined, shut the provider down with a
fresh bounded cleanup context and check its error. The tests own their
provider and follow that cleanup order; the wrapper never installs a global
provider.

## Correlate application logs

Use this bridge with a nonnil, application-owned `slog.Logger` and the handler's
context. Call the returned logger's `InfoContext` or `ErrorContext` methods;
request-derived trace fields do not mutate the base logger or global logging.

```go
// Logger derives a request logger from a nonnil application-owned logger.
// Trace IDs are log correlation fields, never metric labels. No valid span
// means the original logger is returned; neither logger nor globals mutate.
func Logger(ctx context.Context, logger *slog.Logger) *slog.Logger {
    span := trace.SpanContextFromContext(ctx)
    if !span.IsValid() {
        return logger
    }
    return logger.With(
        slog.String("traceId", span.TraceID().String()),
        slog.String("spanId", span.SpanID().String()),
    )
}
```

Ingress spans do not replace
[backend operation diagnostics](../core/diagnostics.md). Do not log credentials,
command payloads or sensitive query arguments, or use tenant/user IDs as metric
labels. Expose metrics, pprof and administrative endpoints only on deliberately
configured routes or listeners.

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
unmapped paths, log correlation without changing the base logger, and the full
mount contract through the wrapper, including direct SSE/WebSocket and shutdown.
Metrics and
exporters are outside the recipe; configure them in your SDK setup.
