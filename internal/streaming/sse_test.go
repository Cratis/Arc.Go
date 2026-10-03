// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/cratis/arc.go/internal/streaming"
)

type sseResponse struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	flushErr  error
	short     bool
}

func (w *sseResponse) SetWriteDeadline(d time.Time) error {
	w.deadlines = append(w.deadlines, d)
	return nil
}
func (w *sseResponse) FlushError() error { w.Flush(); return w.flushErr }
func (w *sseResponse) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return w.ResponseRecorder.Write(p)
}

type unwrappedSSE struct{ http.ResponseWriter }

func (w unwrappedSSE) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestSSEFrameMatchesIndependentFixtureAndResetsEveryDeadline(t *testing.T) {
	fixture, err := os.ReadFile("../../ContractTests/fixtures/v1/observable/direct.sse")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture) < 8 {
		t.Fatal("empty framing fixture")
	}
	w := &sseResponse{ResponseRecorder: httptest.NewRecorder()}
	writer, err := streaming.NewSSEWriter(unwrappedSSE{w}, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(t.Context(), fixture[6:len(fixture)-2]); err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != string(fixture) {
		t.Fatalf("frame = %q, want %q", w.Body.String(), fixture)
	}
	if len(w.deadlines) != 5 || !w.deadlines[0].IsZero() || w.deadlines[1].IsZero() || !w.deadlines[2].IsZero() || w.deadlines[3].IsZero() || !w.deadlines[4].IsZero() {
		t.Fatal(w.deadlines)
	}
}

func TestSSEFlushFailureAndShortWriteAreNotAcknowledged(t *testing.T) {
	failure := errors.New("flush failed")
	for _, tc := range []struct {
		name     string
		flushErr error
		short    bool
		want     error
	}{
		{"flush failure", failure, false, failure}, {"short write", nil, true, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &sseResponse{ResponseRecorder: httptest.NewRecorder()}
			writer, err := streaming.NewSSEWriter(w, 1, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.Start(); err != nil {
				t.Fatal(err)
			}
			w.flushErr, w.short = tc.flushErr, tc.short
			if err := writer.Write(t.Context(), []byte(`{}`)); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v", err)
			}
			if !w.deadlines[len(w.deadlines)-1].IsZero() {
				t.Fatal("write deadline left active")
			}
		})
	}
}

func TestSSEHeadersHTTP2NoStoreAndUnsupportedCapabilities(t *testing.T) {
	w := &sseResponse{ResponseRecorder: httptest.NewRecorder()}
	w.Header().Set("Content-Length", "100")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Cache-Control", "no-store")
	writer, err := streaming.NewSSEWriter(w, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Content-Length") != "" || w.Header().Get("Connection") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	if _, err := streaming.NewSSEWriter(httptest.NewRecorder(), 1, time.Second); !errors.Is(err, http.ErrNotSupported) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := slices.Clone(w.deadlines)
	if err := writer.Write(ctx, []byte(`{}`)); !errors.Is(err, context.Canceled) || len(w.deadlines) != len(before) {
		t.Fatal(err)
	}
}
