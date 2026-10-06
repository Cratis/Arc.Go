// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

type observationLogCapture struct{ record slog.Record }

func (*observationLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *observationLogCapture) Handle(_ context.Context, record slog.Record) error {
	h.record = record.Clone()
	return nil
}
func (h *observationLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *observationLogCapture) WithGroup(string) slog.Handler      { return h }

func TestObservableFailureSanitizesBeforeHostLogHandler(t *testing.T) {
	h := &observationLogCapture{}
	var registry Registry
	built, err := registry.Build(PipelineOptions{Logger: slog.New(h)})
	if err != nil {
		t.Fatal(err)
	}
	p := built.(*queryPipeline)
	failure := errors.New("provider\r\nFORGED\t\x00\x1b\u0085\u2029")
	result := p.observableResult(t.Context(), "query\r\nFORGED\t\x00\x1b\u0085\u2028\u2029", Result[any]{}, failure)
	if !result.HasExceptions() {
		t.Fatal("failure no longer classified as exception")
	}
	values := map[string]string{}
	h.record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "error" {
			logged, ok := attr.Value.Any().(error)
			if !ok || !errors.Is(logged, failure) {
				t.Fatalf("logged error lost its cause: %v", attr.Value)
			}
			values[attr.Key] = logged.Error()
		} else {
			values[attr.Key] = attr.Value.String()
		}
		return true
	})
	if values["query"] != `query\\r\\nFORGED\t\x00\x1b\u0085\u2028\u2029` || values["error"] != `provider\r\nFORGED\t\x00\x1b\u0085\u2029` {
		t.Fatalf("unsafe log attributes: %q", values)
	}
}

func TestObservableFailureDistinguishesLiteralEscapeFromNewline(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"literal escape", `a\nb`, `a\\\\nb`},
		{"newline", "a\nb", `a\\nb`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &observationLogCapture{}
			var registry Registry
			built, err := registry.Build(PipelineOptions{Logger: slog.New(h)})
			if err != nil {
				t.Fatal(err)
			}
			built.(*queryPipeline).observableResult(t.Context(), FullyQualifiedQueryName(tc.input), Result[any]{}, errors.New("provider failed"))
			var got string
			h.record.Attrs(func(attr slog.Attr) bool {
				if attr.Key == "query" {
					got = attr.Value.String()
				}
				return true
			})
			if got != tc.want {
				t.Fatalf("query = %q, want %q", got, tc.want)
			}
		})
	}
}
