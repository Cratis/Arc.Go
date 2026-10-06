// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/metadata"
)

type callbackLogCapture struct{ records []slog.Record }

func (*callbackLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (h *callbackLogCapture) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record.Clone())
	return nil
}
func (h *callbackLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *callbackLogCapture) WithGroup(string) slog.Handler      { return h }

func TestCallbackFailureSanitizesBeforeHostLogHandler(t *testing.T) {
	var registry commands.Registry
	failure := errors.New("callback\r\nFORGED\t\x00\x1b\u0085\u2029")
	must(t, commands.Register[Clear](&registry, commands.Handle(func(Clear, context.Context) (int, error) {
		return 0, failure
	}), commands.WithAuthorization[Clear](metadata.Authorization{AllowAnonymous: true})))
	h := &callbackLogCapture{}
	logger := slog.New(h)
	p := build(t, &registry, commands.PipelineOptions{Logger: logger})
	id, err := correlation.Parse("acb94783-78c1-43df-b740-a3d5746189e1")
	must(t, err)
	result, err := p.Execute(correlation.WithID(t.Context(), id), Clear{})
	if !errors.Is(err, failure) || !result.HasExceptions() {
		t.Fatalf("command failure changed: result=%+v, error=%v", result, err)
	}
	found := false
	for _, record := range h.records {
		if record.Message != "Command callback failed" {
			continue
		}
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "error" {
				found = true
				logged, ok := attr.Value.Any().(error)
				if !ok || !errors.Is(logged, failure) || logged.Error() != `callback\r\nFORGED\t\x00\x1b\u0085\u2029` {
					t.Fatalf("unsafe or unclassified logged error: %v", attr.Value)
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("callback failure log missing error attribute")
	}
	// Building the pipeline must not replace or sanitize the borrowed host logger.
	logger.ErrorContext(t.Context(), "host message", "error", failure)
	h.records[len(h.records)-1].Attrs(func(attr slog.Attr) bool {
		if attr.Key == "error" && attr.Value.Any() != failure {
			t.Fatal("host logger was mutated")
		}
		return true
	})
}
