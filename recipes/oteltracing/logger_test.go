// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package oteltracing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/recipes/internal/fixture"
	"github.com/cratis/arc.go/recipes/oteltracing"
)

func TestRequestLogsJoinTheServerSpanWithoutChangingTheBaseLogger(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	recorder := tracetest.NewSpanRecorder()
	// The application owns the provider and shuts it down after Arc and HTTP.
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	logged := fixture.New(t, arc.Options{Logger: logger}, func(builder *arc.Builder) error {
		return queries.Register[observedTrace](builder, "Logged", queries.Function(func(ctx context.Context, _ queries.NoArguments) (observedTrace, error) {
			oteltracing.Logger(ctx, logger).InfoContext(ctx, "queried")
			span := trace.SpanContextFromContext(ctx)
			return observedTrace{TraceID: span.TraceID().String(), SpanID: span.SpanID().String()}, nil
		}), queries.WithPath[queries.NoArguments](tracePath))
	})
	handler := oteltracing.Instrument(logged.App, provider, propagation.TraceContext{})
	r := fixture.Do(t, fixture.Serve(t, handler), http.MethodGet, tracePath, "", http.Header{"Traceparent": {traceparent}})
	if r.Status != http.StatusOK {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
	span := spanNamed(t, recorder, "GET "+tracePath)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["traceId"] != traceID || record["spanId"] != span.SpanContext().SpanID().String() || record["msg"] != "queried" {
		t.Fatalf("log record = %v", record)
	}
	output.Reset()
	logger.Info("outside request")
	if bytes.Contains(output.Bytes(), []byte("traceId")) || bytes.Contains(output.Bytes(), []byte("spanId")) {
		t.Fatalf("base logger was changed: %s", output.Bytes())
	}
}

func TestNoSpanReturnsTheOriginalLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if got := oteltracing.Logger(t.Context(), logger); got != logger {
		t.Fatal("logger without a span was replaced")
	}
}
