// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	boundary "github.com/cratis/arc.go/internal/pipeline"
)

// WaitOptions controls subject-equivalent snapshots. Zero disables waiting;
// Timeout zero/negative uses thirty seconds. The pipeline applies MaximumWait.
type WaitOptions struct {
	ForFirstResult bool
	Timeout        time.Duration
}

// WithWait returns an independent request with observable wait controls.
func (r Request) WithWait(options WaitOptions) Request { r.wait = options; return r }

// Wait returns observable controls by value, never bound as application arguments.
func (r Request) Wait() WaitOptions { return r.wait }

func parseWait(controls map[string]string) WaitOptions {
	w := WaitOptions{ForFirstResult: strings.EqualFold(strings.TrimSpace(controls["waitforfirstresult"]), "true"), Timeout: 30 * time.Second}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(controls["waitforfirstresulttimeout"]), 64)
	if err == nil && seconds > 0 && !math.IsInf(seconds, 0) && !math.IsNaN(seconds) {
		ns := seconds * float64(time.Second)
		if ns >= float64(math.MaxInt64) {
			w.Timeout = time.Duration(math.MaxInt64)
		} else if ns >= 1 {
			w.Timeout = time.Duration(ns)
		}
	}
	return w
}
func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Second), 'f', -1, 64)
}

func (p *queryPipeline) observableSnapshot(ctx context.Context, name FullyQualifiedQueryName, request Request) (Result[any], error) {
	q, _ := p.Lookup(name)
	probe := boundary.IsObservationProbe(ctx)
	o, result, err := p.openObservation(ctx, name, request, !q.enumerable && !probe)
	if o == nil {
		return result, err
	}
	if probe {
		result = NotReady[any](o.metadata.correlationID)
	} else if q.enumerable {
		// Admission still protects the capability error; no source is activated.
		err = ErrEnumerableRequiresStreaming
		result = p.observableResult(ctx, name, result, err)
	} else {
		result, err = o.snapshot(ctx, request.wait)
	}
	if cleanupErr := o.cleanup(); cleanupErr != nil {
		err = errors.Join(err, cleanupErr)
		result = p.observableResult(ctx, name, result, cleanupErr)
	}
	return result, err
}
func (o *Observation) snapshot(ctx context.Context, wait WaitOptions) (Result[any], error) {
	if err := o.begin(ctx); err != nil {
		return o.pipeline.observableResult(ctx, o.metadata.name, o.admission, err), err
	}
	defer o.end()
	result := NotReady[any](o.metadata.correlationID)
	work, cancel := context.WithCancel(o.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	if wait.ForFirstResult {
		timeout := wait.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		if timeout > o.pipeline.options.MaximumWait {
			timeout = o.pipeline.options.MaximumWait
		}
		waiting, done := context.WithTimeout(work, timeout)
		defer done()
		for {
			var value any
			err := boundary.Call(waiting, func(ctx context.Context) error { var err error; value, err = o.stream.next(ctx); return err })
			if err != nil {
				if work.Err() != nil {
					err = work.Err()
				} else if waiting.Err() == context.DeadlineExceeded {
					err = &WaitTimeoutError{Timeout: timeout}
				} else if errors.Is(err, io.EOF) {
					err = ErrCompletedWithoutResult
				}
				return o.pipeline.observableResult(work, o.metadata.name, result, err), err
			}
			candidate, verdict, err := o.candidate(waiting, value)
			if verdict == Suppress && err == nil {
				continue
			}
			return candidate, err
		}
	}
	if o.source.current == nil {
		return result, nil
	}
	var value any
	var present bool
	err := boundary.Call(work, func(ctx context.Context) error {
		var err error
		value, present, err = o.source.current(ctx)
		return err
	})
	if err != nil {
		return o.pipeline.observableResult(work, o.metadata.name, result, err), err
	}
	if !present {
		return result, nil
	}
	candidate, verdict, err := o.candidate(work, value)
	if verdict == Suppress && err == nil {
		return result, nil
	}
	return candidate, err
}
