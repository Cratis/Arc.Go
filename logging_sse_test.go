// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type flushFailureResponse struct {
	*httptest.ResponseRecorder
	failure error
}

func (w flushFailureResponse) FlushError() error { return w.failure }

func TestObservedWriterPreservesFlushFailureAcknowledgement(t *testing.T) {
	failure := errors.New("flush failed")
	base := &responseWriter{ResponseWriter: flushFailureResponse{httptest.NewRecorder(), failure}}
	if err := http.NewResponseController(observingWriter(base)).Flush(); !errors.Is(err, failure) {
		t.Fatal("logging wrapper lost flush failure", err)
	}
	if base.status != 200 {
		t.Fatal(base.status)
	}
}

type unwrapOnlyWriter struct{ http.ResponseWriter }

func (w unwrapOnlyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type deadlineResponse struct {
	*httptest.ResponseRecorder
	set bool
}

func (w *deadlineResponse) SetWriteDeadline(time.Time) error { w.set = true; return nil }

func TestObservedWriterUnwrapPreservesDeadlineAndFlushThroughMiddleware(t *testing.T) {
	response := &deadlineResponse{ResponseRecorder: httptest.NewRecorder()}
	base := &responseWriter{ResponseWriter: unwrapOnlyWriter{response}}
	controller := http.NewResponseController(observingWriter(base))
	if err := controller.SetWriteDeadline(time.Time{}); err != nil || !response.set {
		t.Fatal(err)
	}
	if err := controller.Flush(); err != nil || !response.Flushed {
		t.Fatal(err)
	}
}
