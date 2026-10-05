// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// SSEWriter is a synchronous, single-owner writer. Success acknowledges a whole
// frame write and flush, not browser receipt. Wrappers must expose Unwrap or
// FlushError/Flusher and SetWriteDeadline; unsupported capabilities fail before
// commitment. No writes are permitted after the owning handler returns.
type SSEWriter struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	protocol   int
	timeout    time.Duration
}

// NewSSEWriter checks streaming capabilities and clears the absolute unary write
// deadline. Start must follow successful query admission. Keepalives, if enabled
// by the caller, must use the same owner; no background writer is started.
func NewSSEWriter(w http.ResponseWriter, protocol int, timeout time.Duration) (*SSEWriter, error) {
	if w == nil || timeout <= 0 || !canFlush(w) {
		return nil, http.ErrNotSupported
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return &SSEWriter{writer: w, controller: controller, protocol: protocol, timeout: timeout}, nil
}

func canFlush(w http.ResponseWriter) bool {
	// Bound traversal to avoid a malformed wrapper cycle hanging admission.
	for range 128 {
		switch value := w.(type) {
		case interface{ FlushError() error }:
			return true
		case http.Flusher:
			return true
		case interface{ Unwrap() http.ResponseWriter }:
			w = value.Unwrap()
		default:
			return false
		}
	}
	return false
}

// Start commits and flushes SSE headers with a bounded write deadline. HTTP/2
// omits the HTTP/1.x hop-by-hop Connection header. No pending result is invented.
func (w *SSEWriter) Start() error {
	h := w.writer.Header()
	h.Del("Content-Length")
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	if cache := h.Get("Cache-Control"); !strings.Contains(cache, "no-store") && !strings.Contains(cache, "private") {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("X-Accel-Buffering", "no")
	if w.protocol == 1 {
		h.Set("Connection", "keep-alive")
	} else {
		h.Del("Connection")
	}
	return w.write(nil)
}

// Write encodes a complete data frame before touching the transport. The caller
// supplies compact JSON with no literal newline; no event names or IDs are added.
func (w *SSEWriter) Write(ctx context.Context, json []byte) error {
	if ctx == nil {
		return errors.New("SSE write requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	frame := make([]byte, 0, len(json)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, json...)
	frame = append(frame, '\n', '\n')
	return w.write(frame)
}

func (w *SSEWriter) write(frame []byte) error {
	if err := w.controller.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil {
		return err
	}
	var err error
	if frame != nil {
		var n int
		n, err = w.writer.Write(frame)
		if err == nil && n != len(frame) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = w.controller.Flush()
	}
	// Never carry a per-frame deadline into idle waiting for the next emission.
	return errors.Join(err, w.controller.SetWriteDeadline(time.Time{}))
}
