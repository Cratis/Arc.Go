// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package logging_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/cratis/arc.go/internal/logging"
)

type captureHandler struct {
	record slog.Record
	attrs  []slog.Attr
	group  string
	err    error
	ctx    context.Context
}

func (*captureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}
func (h *captureHandler) Handle(ctx context.Context, record slog.Record) error {
	h.record, h.ctx = record.Clone(), ctx
	return h.err
}
func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(h.attrs, attrs...)
	return h
}
func (h *captureHandler) WithGroup(name string) slog.Handler { h.group = name; return h }

type diagnosticValue string

func (v diagnosticValue) String() string { return string(v) }

type diagnosticLogValue struct{}

func (diagnosticLogValue) LogValue() slog.Value { return slog.StringValue("lazy\r\nvalue") }

type diagnosticError struct{}

func (*diagnosticError) Error() string { return "cause\r\nFORGED" }

func TestHandlerSanitizesGroupsAndPreservesErrorChains(t *testing.T) {
	h := &captureHandler{}
	logger := logging.Sanitize(slog.New(h))
	cause := &diagnosticError{}
	failure := fmt.Errorf("provider: %w", cause)
	attrs := []slog.Attr{slog.Group("group\r\n", slog.String("key\r\n", "text\r\n"), slog.Any("error", failure), slog.Any("stringer", diagnosticValue("id\r\n")), slog.Any("lazy", diagnosticLogValue{}), slog.Int("number", 42))}
	logger.WithGroup("scope\r\n").With(attrs[0]).WarnContext(t.Context(), "message\r\n", attrs[0])
	if h.group != "scope" || h.record.Message != "message" || len(h.attrs) != 1 {
		t.Fatalf("unsafe group/message: %q, %q, %v", h.group, h.record.Message, h.attrs)
	}
	check := func(attr slog.Attr) {
		t.Helper()
		if attr.Key != "group" {
			t.Fatal(attr)
		}
		children := attr.Value.Group()
		if children[0].Key != "key" || children[0].Value.String() != "text" || children[2].Value.Any().(fmt.Stringer).String() != "id" || children[3].Value.String() != "lazyvalue" || children[4].Value.Int64() != 42 {
			t.Fatalf("unsafe grouped attributes: %v", children)
		}
		logged, ok := children[1].Value.Any().(error)
		var typed *diagnosticError
		if !ok || logged.Error() != "provider: causeFORGED" || !errors.Is(logged, cause) || !errors.As(logged, &typed) || typed != cause {
			t.Fatalf("logged error lost sanitized text or cause: %v", children[1])
		}
	}
	check(h.attrs[0])
	h.record.Attrs(func(attr slog.Attr) bool { check(attr); return true })
	if attrs[0].Key != "group\r\n" || attrs[0].Value.Group()[0].Value.String() != "text\r\n" {
		t.Fatal("caller attributes were mutated")
	}
	if logging.Sanitize(logger) != logger || logging.Sanitize(nil) != nil {
		t.Fatal("nil or already wrapped logger changed")
	}
}

func TestHandlerPreservesMetadataContextAndHostFailure(t *testing.T) {
	failure := errors.New("host failure")
	h := &captureHandler{err: failure}
	handler := logging.Sanitize(slog.New(h)).Handler()
	ctx := t.Context()
	if handler.Enabled(ctx, slog.LevelInfo) || !handler.Enabled(ctx, slog.LevelWarn) {
		t.Fatal("host level policy changed")
	}
	record := slog.NewRecord(time.Now(), slog.LevelWarn, "message\n", 123)
	record.AddAttrs(slog.String("value", "text\n"))
	if err := handler.Handle(ctx, record); !errors.Is(err, failure) {
		t.Fatalf("host error = %v", err)
	}
	if h.ctx != ctx || h.record.Time != record.Time || h.record.Level != record.Level || h.record.PC != record.PC {
		t.Fatal("record metadata or context changed")
	}
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Value.String() != "text\n" {
			t.Fatal("caller record was mutated")
		}
		return true
	})
}
