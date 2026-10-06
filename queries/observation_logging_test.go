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
	p := &queryPipeline{options: PipelineOptions{Logger: slog.New(h)}}
	failure := errors.New("provider\r\nFORGED\t\x00\x1b\u0085\u2029")
	result := p.observableResult(t.Context(), "query\r\nFORGED", Result[any]{}, failure)
	if !result.HasExceptions() {
		t.Fatal("failure no longer classified as exception")
	}
	values := map[string]string{}
	h.record.Attrs(func(attr slog.Attr) bool { values[attr.Key] = attr.Value.String(); return true })
	if values["query"] != "queryFORGED" || values["error"] != "providerFORGED" {
		t.Fatalf("unsafe log attributes: %q", values)
	}
}
