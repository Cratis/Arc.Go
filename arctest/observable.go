// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arctest

import (
	"context"
	"errors"
	"time"

	"github.com/cratis/arc.go/queries"
)

// ErrCaptureCapacity identifies result bytes exceeding an observable capture limit.
// Earlier results are returned alongside the error; no oversized result is retained.
var ErrCaptureCapacity = errors.New("arctest observable capture byte capacity exhausted")

// CaptureOptions bounds an in-process full-result observation. Zero fields select
// 16 results, 1 MiB of encoded results, and a five-second observation timeout.
// Reaching MaxResults is successful completion, not a dropped emission. Timeout
// and source/pipeline/cleanup failures remain errors with earlier captured results.
// These limits bound test-kit retention, not allocations in application callbacks.
type CaptureOptions struct {
	MaxResults int
	MaxBytes   int64
	Timeout    time.Duration
}

// ObservableQueryScenario captures full results from the real query pipeline,
// including admission, rendering, interception and emission guards. It borrows a
// Scenario; it tests neither HTTP binding nor SSE/WebSocket/hub transfer modes.
// Capture is synchronous and owns its source cancellation and cleanup join.
type ObservableQueryScenario[R any] struct {
	scenario *Scenario
	name     queries.FullyQualifiedQueryName
}

// NewObservableQuery selects a registered observable query and expected data R.
// Unknown names and incompatible result types fail before source activation.
func NewObservableQuery[R any](scenario *Scenario, name queries.FullyQualifiedQueryName) *ObservableQueryScenario[R] {
	return &ObservableQueryScenario[R]{scenario: scenario, name: name}
}

var errCaptureComplete = errors.New("arctest observable capture complete")

// Capture returns at source completion, failure, timeout/cancellation, or after
// MaxResults acknowledged results. It runs no detached test-kit goroutine. A
// successful return proves the owned observation joined; cleanup timeouts remain
// inspectable and retained by the application's pipeline for Scenario.Close retry.
// Results are subscriber-local snapshots; callers own them after Capture returns.
// ctx supplies trusted identity, tenant and correlation metadata.
func (s *ObservableQueryScenario[R]) Capture(ctx context.Context, request queries.Request, options CaptureOptions) ([]queries.Result[R], error) {
	if s == nil || s.scenario == nil || ctx == nil || options.MaxResults < 0 || options.MaxBytes < 0 || options.Timeout < 0 {
		return nil, queries.ErrInvalidRegistration
	}
	if options.MaxResults == 0 {
		options.MaxResults = 16
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 1 << 20
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	work, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	results := []queries.Result[R]{}
	var bytes int64
	err := queries.Subscribe[R](work, s.scenario.application.Queries(), s.name, request, func(result queries.Result[R]) error {
		encoded, err := result.MarshalJSON()
		if err != nil {
			return err
		}
		if int64(len(encoded)) > options.MaxBytes-bytes {
			return ErrCaptureCapacity
		}
		bytes += int64(len(encoded))
		results = append(results, result)
		if len(results) == options.MaxResults {
			return errCaptureComplete
		}
		return nil
	})
	return results, withoutCaptureComplete(err)
}

// Only remove our explicit stop marker, never an accompanying provider/cleanup
// failure. errors.Is alone would hide significant errors joined with that marker.
func withoutCaptureComplete(err error) error {
	if err == errCaptureComplete {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var remaining error
		for _, part := range joined.Unwrap() {
			remaining = errors.Join(remaining, withoutCaptureComplete(part))
		}
		return remaining
	}
	return err
}
