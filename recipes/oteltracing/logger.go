// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package oteltracing

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// recipe:start otel-logger

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

// recipe:end
