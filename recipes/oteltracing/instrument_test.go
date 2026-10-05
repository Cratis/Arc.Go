// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package oteltracing_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/recipes/internal/fixture"
	"github.com/cratis/arc.go/recipes/oteltracing"
)

const (
	tracePath   = "/api/diagnostics/trace"
	traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	parentID    = "00f067aa0ba902b7"
	traceparent = "00-" + traceID + "-" + parentID + "-01"
)

type observedTrace struct {
	TraceID string `json:"traceId"`
	SpanID  string `json:"spanId"`
}

func registerTraceQuery(builder *arc.Builder) error {
	return queries.Register[observedTrace](builder, "Current", queries.Function(func(ctx context.Context, _ queries.NoArguments) (observedTrace, error) {
		span := trace.SpanContextFromContext(ctx)
		return observedTrace{TraceID: span.TraceID().String(), SpanID: span.SpanID().String()}, nil
	}), queries.WithPath[queries.NoArguments](tracePath))
}

func setup(t *testing.T) (*fixture.Application, *tracetest.SpanRecorder, http.Handler) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	f := fixture.New(t, arc.Options{}, registerTraceQuery)
	return f, recorder, oteltracing.Instrument(f.App, provider, propagation.TraceContext{})
}

func spanNamed(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	var names []string
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
		names = append(names, span.Name())
	}
	t.Fatalf("no span %q in %q", name, names)
	return nil
}

func attributeValue(span sdktrace.ReadOnlySpan, key attribute.Key) (attribute.Value, bool) {
	for _, kv := range span.Attributes() {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestInstrumentedMountPreservesArcBehavior(t *testing.T) {
	f, _, handler := setup(t)
	fixture.VerifyMount(t, f, fixture.Serve(t, handler))
}

func TestIncomingTraceContextReachesArcHandlers(t *testing.T) {
	_, recorder, handler := setup(t)
	server := fixture.Serve(t, handler)
	r := fixture.Do(t, server, http.MethodGet, tracePath, "", http.Header{"Traceparent": {traceparent}})
	if r.Status != http.StatusOK || !strings.Contains(r.Body, `"traceId":"`+traceID+`"`) {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
	span := spanNamed(t, recorder, "GET "+tracePath)
	if span.Parent().SpanID().String() != parentID || span.SpanContext().TraceID().String() != traceID {
		t.Fatalf("parent %v, trace %v", span.Parent().SpanID(), span.SpanContext().TraceID())
	}
	if !strings.Contains(r.Body, `"spanId":"`+span.SpanContext().SpanID().String()+`"`) {
		t.Fatalf("handler did not observe the server span: %q", r.Body)
	}
}

func TestQuerySpansKeepTheirMethod(t *testing.T) {
	_, recorder, handler := setup(t)
	server := fixture.Serve(t, handler)
	if r := fixture.Do(t, server, "QUERY", fixture.QueryPath, `{"arguments":{"title":"traced"}}`, nil); r.Status != http.StatusOK {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
	span := spanNamed(t, recorder, "QUERY "+fixture.QueryPath)
	// Semantic conventions only know the RFC 9110 methods: QUERY is recorded
	// as _OTHER with the original method beside it.
	method, _ := attributeValue(span, "http.request.method")
	original, _ := attributeValue(span, "http.request.method_original")
	if method.AsString() != "_OTHER" || original.AsString() != "QUERY" {
		t.Fatalf("method %q, original %q", method.AsString(), original.AsString())
	}
}

func TestUnmappedPathsDoNotNameSpans(t *testing.T) {
	_, recorder, handler := setup(t)
	server := fixture.Serve(t, handler)
	fixture.Do(t, server, http.MethodGet, "/api/random-1234", "", nil)
	fixture.Do(t, server, "PROPFIND", "/api/random-5678", "", nil)
	get := spanNamed(t, recorder, http.MethodGet)
	if status, _ := attributeValue(get, "http.response.status_code"); status.AsInt64() != http.StatusNotFound {
		t.Fatalf("status %v", status.AsInt64())
	}
	spanNamed(t, recorder, "HTTP")
}
