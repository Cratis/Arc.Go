// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/cratis/arc.go/concepts"
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
	if h.group != `scope\r\n` || h.record.Message != `message\r\n` || len(h.attrs) != 1 {
		t.Fatalf("unsafe group/message: %q, %q, %v", h.group, h.record.Message, h.attrs)
	}
	check := func(attr slog.Attr) {
		t.Helper()
		if attr.Key != `group\r\n` {
			t.Fatal(attr)
		}
		children := attr.Value.Group()
		if children[0].Key != `key\r\n` || children[0].Value.String() != `text\r\n` || children[2].Value.Any().(fmt.Stringer).String() != `id\r\n` || children[3].Value.String() != `lazy\r\nvalue` || children[4].Value.Int64() != 42 {
			t.Fatalf("unsafe grouped attributes: %v", children)
		}
		logged, ok := children[1].Value.Any().(error)
		var typed *diagnosticError
		if !ok || logged.Error() != `provider: cause\r\nFORGED` || !errors.Is(logged, cause) || !errors.As(logged, &typed) || typed != cause {
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

func TestHandlerEscapesRawAndAlreadyEscapedValuesIdentically(t *testing.T) {
	const input = "text\\literal\r\n\t\u202e"
	const want = `text\\literal\r\n\t\u202e`
	h := &captureHandler{}
	logger := logging.Sanitize(slog.New(h))
	attrs := []slog.Attr{slog.Group("values", slog.String("raw", input), slog.Any("escaped", logging.Escaped(logging.String(input))))}
	logger.With(attrs[0]).WarnContext(t.Context(), "diagnostic", attrs[0])
	check := func(attr slog.Attr) {
		t.Helper()
		for _, child := range attr.Value.Group() {
			if child.Value.Kind() != slog.KindString || child.Value.String() != want {
				t.Fatalf("%s = %v, want string %q", child.Key, child.Value, want)
			}
		}
	}
	check(h.attrs[0])
	h.record.Attrs(func(attr slog.Attr) bool { check(attr); return true })
}

func TestHandlerLogsJoinedErrorsWithVisibleSeparator(t *testing.T) {
	h := &captureHandler{}
	logger := logging.Sanitize(slog.New(h))
	provider := errors.New("provider failed")
	cleanup := errors.New("cleanup timed out")
	logger.ErrorContext(t.Context(), "query failed", "error", errors.Join(provider, cleanup))
	found := false
	h.record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "error" {
			found = true
			logged, ok := attr.Value.Any().(error)
			if !ok || logged.Error() != `provider failed\ncleanup timed out` || !errors.Is(logged, provider) || !errors.Is(logged, cleanup) {
				t.Fatalf("joined error lost its separator or causes: %v", attr.Value)
			}
		}
		return true
	})
	if !found {
		t.Fatal("log missing error attribute")
	}
}

func TestHandlerPreservesStringerJSONValues(t *testing.T) {
	const uuidText = "acb94783-78c1-43df-b740-a3d5746189e1"
	id, err := concepts.ParseUUID(uuidText)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"UUID", id, uuidText},
		{"control characters", diagnosticValue("id\r\nFORGED\t\x00"), `id\r\nFORGED\t\x00`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := logging.Sanitize(slog.New(slog.NewJSONHandler(&output, nil)))
			logger.InfoContext(t.Context(), "diagnostic", "correlationId", tc.value)
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if got := record["correlationId"]; got != tc.want {
				t.Fatalf("correlationId = %#v, want %q; log = %s", got, tc.want, output.String())
			}
		})
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
