// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

type requestLogCapture struct {
	record  slog.Record
	records []slog.Record
}

func (*requestLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *requestLogCapture) Handle(_ context.Context, record slog.Record) error {
	h.record = record.Clone()
	h.records = append(h.records, record.Clone())
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
			if got, want := attr.Value.String(), `GET\r\nFORGED\x00\x1b\u0085\u2028`; got != want {
				t.Errorf("method = %q, want %q", got, want)
			}
		}
		return true
	})
	if !found {
		t.Fatal("completion log missing method attribute")
	}
}

type loggingReadModel struct {
	Name string `json:"name"`
}

func TestSnapshotFailureSanitizesBeforeHostLogHandler(t *testing.T) {
	h := &requestLogCapture{}
	b, err := arc.NewBuilder(arc.Options{Logger: slog.New(h)})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("argument\r\nFORGED\t\x00\x1b\u0085\u2029")
	if err := queries.Register[loggingReadModel](b, "Current", queries.Function(func(context.Context, queries.NoArguments) (loggingReadModel, error) {
		return loggingReadModel{}, failure
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Queries().Perform(t.Context(), "loggingReadModel.Current", queries.Request{})
	if !errors.Is(err, failure) || !result.HasExceptions() {
		t.Fatalf("query failure changed: result=%+v, error=%v", result, err)
	}
	found := false
	for _, record := range h.records {
		if record.Message != "query failed" {
			continue
		}
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "error" {
				found = true
				logged, ok := attr.Value.Any().(error)
				if !ok || !errors.Is(logged, failure) || logged.Error() != `argument\r\nFORGED\t\x00\x1b\u0085\u2029` {
					t.Fatalf("unsafe or unclassified logged error: %v", attr.Value)
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("snapshot failure log missing error attribute")
	}
}
