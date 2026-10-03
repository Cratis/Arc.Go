// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/cratis/arc.go/correlation"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/queries"
)

func (a *Application) directSSE(w http.ResponseWriter, r *http.Request, name queries.FullyQualifiedQueryName, request queries.Request) {
	p, ok := a.queries.(queries.ObservablePipeline)
	if !ok {
		a.publish(w, r, http.StatusInternalServerError, queries.FromError[any](correlation.FromContext(r.Context()), queries.ErrObservableCapability))
		return
	}
	o, admission, err := p.Open(r.Context(), name, request)
	if o == nil {
		status := admission.StatusCode()
		if errors.Is(err, queries.ErrObservationCapacity) {
			status = http.StatusTooManyRequests
		}
		if errors.Is(err, queries.ErrObservationsStopping) {
			status = http.StatusServiceUnavailable
		}
		a.publish(w, r, status, admission)
		return
	}
	defer func() {
		ctx, cancel, err := boundary.CleanupContext(r.Context(), a.options.Observable.CloseGrace)
		if err == nil {
			defer cancel()
			err = o.Close(ctx)
		}
		if err != nil {
			a.hostFailure(r.Context(), "observable SSE cleanup failed", err)
		}
	}()
	writer, err := streaming.NewSSEWriter(w, r.ProtoMajor, a.options.Observable.WriteTimeout)
	if err != nil {
		a.hostFailure(r.Context(), "observable SSE capability unavailable", err)
		a.publish(w, r, http.StatusInternalServerError, queries.FromError[any](correlation.FromContext(r.Context()), err))
		return
	}
	if err := writer.Start(); err != nil {
		a.hostFailure(r.Context(), "observable SSE opening interrupted", err)
		return // Start may already have committed headers; never write unary JSON behind it.
	}
	err = o.Run(r.Context(), queries.ObservationOptions{TransferMode: queries.Full, SkipEnumerableNull: true}, func(result queries.Result[any]) error {
		body, encodeErr := encode(r.Context(), result)
		if encodeErr != nil || int64(len(body))+8 > a.options.HTTP.MaxResponseBytes {
			if encodeErr == nil {
				encodeErr = fmt.Errorf("observable SSE frame exceeds response limit")
			}
			// A failed candidate is never acknowledged as delivered. A single safe
			// terminal result is best effort, and the source remains application-owned.
			failure, _ := publicationFailure(result)
			body, fallbackErr := encode(r.Context(), failure)
			if fallbackErr == nil && int64(len(body))+8 <= a.options.HTTP.MaxResponseBytes {
				fallbackErr = writer.Write(r.Context(), body)
			}
			return errors.Join(encodeErr, fallbackErr)
		}
		return writer.Write(r.Context(), body)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		a.hostFailure(r.Context(), "observable SSE delivery failed", err)
	}
}
