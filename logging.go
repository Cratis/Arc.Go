// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"time"
)

type responseWriter struct {
	http.ResponseWriter
	status   int
	hijacked bool
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(body)
}

type flushCapability interface {
	http.Flusher
	FlushError() error
}

type flushWriter struct{ w *responseWriter }

func (w flushWriter) Flush() {
	// The legacy Flusher interface cannot return an error. ResponseController
	// uses FlushError instead, preserving streaming delivery acknowledgements.
	_ = w.FlushError()
}
func (w flushWriter) FlushError() error {
	if w.w.status == 0 {
		w.w.WriteHeader(200)
	}
	return http.NewResponseController(w.w.ResponseWriter).Flush()
}

type hijackWriter struct{ w *responseWriter }

func (w hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := w.w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		w.w.hijacked = true
	}
	return conn, buffer, err
}

type pushWriter struct{ w *responseWriter }

func (w pushWriter) Push(target string, options *http.PushOptions) error {
	return w.w.ResponseWriter.(http.Pusher).Push(target, options)
}
func observingWriter(base *responseWriter) http.ResponseWriter {
	_, flush := base.ResponseWriter.(http.Flusher)
	_, hijack := base.ResponseWriter.(http.Hijacker)
	_, push := base.ResponseWriter.(http.Pusher)
	f := flushWriter{base}
	h := hijackWriter{base}
	p := pushWriter{base}
	switch {
	case flush && hijack && push:
		return struct {
			*responseWriter
			flushCapability
			http.Hijacker
			http.Pusher
		}{base, f, h, p}
	case flush && hijack:
		return struct {
			*responseWriter
			flushCapability
			http.Hijacker
		}{base, f, h}
	case flush && push:
		return struct {
			*responseWriter
			flushCapability
			http.Pusher
		}{base, f, p}
	case hijack && push:
		return struct {
			*responseWriter
			http.Hijacker
			http.Pusher
		}{base, h, p}
	case flush:
		return struct {
			*responseWriter
			flushCapability
		}{base, f}
	case hijack:
		return struct {
			*responseWriter
			http.Hijacker
		}{base, h}
	case push:
		return struct {
			*responseWriter
			http.Pusher
		}{base, p}
	default:
		return base
	}
}
func (a *Application) serveObserved(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	base := &responseWriter{ResponseWriter: w}
	a.serveIngress(observingWriter(base), r, base)
	if a.options.Logger == nil {
		return
	}
	status := base.status
	if status == 0 {
		status = 200
	}
	route := "unmatched"
	if _, exists := a.routeTable[r.URL.Path]; exists {
		route = r.URL.Path
	} else if _, pattern := a.rawMux.Handler(r); pattern != "" {
		route = pattern
	}
	// Escape backslashes before line breaks at the sink as well as in the sanitizing logger wrapper.
	route = strings.ReplaceAll(route, `\`, `\\`)
	route = strings.ReplaceAll(route, "\r", `\r`)
	route = strings.ReplaceAll(route, "\n", `\n`)
	a.options.Logger.DebugContext(r.Context(), "Arc HTTP request completed", "method", r.Method, "route", route, "status", status, "duration", time.Since(started), "correlationId", w.Header().Get(a.options.HTTP.CorrelationHeader))
}
