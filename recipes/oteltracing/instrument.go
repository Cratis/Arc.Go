// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package oteltracing wraps an Arc application with otelhttp server spans
// whose names stay readable for QUERY and bounded for unknown paths.
package oteltracing

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	arc "github.com/cratis/arc.go"
)

// recipe:start otel-instrument

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

// recipe:end
