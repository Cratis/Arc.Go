// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"

	arc "github.com/cratis/arc.go"
)

type requestLogCapture struct{ record slog.Record }

func (*requestLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *requestLogCapture) Handle(_ context.Context, record slog.Record) error {
	h.record = record.Clone()
	return nil
}
func (h *requestLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *requestLogCapture) WithGroup(string) slog.Handler      { return h }

func TestRequestCompletionSanitizesBeforeHostLogHandler(t *testing.T) {
	h := &requestLogCapture{}
	b, err := arc.NewBuilder(arc.Options{Logger: slog.New(h)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/unmatched", nil)
	r.Method = "GET\r\nFORGED\x00\x1b\u0085\u2028"
	a.ServeHTTP(httptest.NewRecorder(), r)
	found := false
	h.record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "method" {
			found = true
			if got := attr.Value.String(); got != "GETFORGED" {
				t.Errorf("method = %q, want GETFORGED", got)
			}
		}
		return true
	})
	if !found {
		t.Fatal("completion log missing method attribute")
	}
}
