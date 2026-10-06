// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package logging

import (
	"context"
	"fmt"
	"log/slog"
)

// Sanitize wraps a borrowed logger without changing its level policy or the host
// logger. Nil remains nil; already wrapped loggers are returned unchanged.
func Sanitize(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return nil
	}
	if _, ok := logger.Handler().(*handler); ok {
		return logger
	}
	return slog.New(&handler{next: logger.Handler()})
}

type handler struct{ next slog.Handler }

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, String(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(sanitizeAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		clean[i] = sanitizeAttr(attr)
	}
	return &handler{next: h.next.WithAttrs(clean)}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(String(name))}
}

func sanitizeAttr(attr slog.Attr) slog.Attr {
	attr.Key = String(attr.Key)
	value := attr.Value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		value = slog.StringValue(String(value.String()))
	case slog.KindGroup:
		attrs := value.Group()
		clean := make([]slog.Attr, len(attrs))
		for i, child := range attrs {
			clean[i] = sanitizeAttr(child)
		}
		value = slog.GroupValue(clean...)
	case slog.KindAny:
		switch v := value.Any().(type) {
		case error:
			value = slog.AnyValue(sanitizedError{err: v})
		case fmt.Stringer:
			value = slog.AnyValue(sanitizedStringer{value: v})
		}
	}
	attr.Value = value
	return attr
}

// Keep error classification and wrapped causes available to host handlers.
type sanitizedError struct{ err error }

func (e sanitizedError) Error() string { return String(e.err.Error()) }
func (e sanitizedError) Unwrap() error { return e.err }

type sanitizedStringer struct{ value fmt.Stringer }

func (s sanitizedStringer) String() string { return String(s.value.String()) }
