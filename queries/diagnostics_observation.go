// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"time"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
)

// Observation.mu protects these scalars alongside native lifecycle state.
type observationDiagnostics struct {
	recorder                    *observability.Recorder
	label                       string
	started, consuming, cleanup time.Time
	outcome                     observability.Outcome
	terminal                    bool
}

func (p *queryPipeline) observationDiagnostics(name FullyQualifiedQueryName) *observationDiagnostics {
	if p.options.Diagnostics == nil {
		return nil
	}
	return &observationDiagnostics{recorder: p.options.Diagnostics, label: p.options.Diagnostics.Label(string(name)), started: time.Now()}
}

func (d *observationDiagnostics) record(phase observability.Phase, outcome observability.Outcome, started time.Time) {
	if d == nil {
		return
	}
	var seconds float64
	if !started.IsZero() {
		seconds = time.Since(started).Seconds()
	}
	d.recorder.Record(observability.Observation{Artifact: d.label, Operation: observability.Query, Transport: observability.ObservableTransport, Phase: phase, Outcome: outcome, Seconds: seconds})
}

func (o *Observation) openingFailed(ctx context.Context, result Result[any], err error) {
	if o.diagnostic == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if d := o.diagnostic; d != nil {
		d.terminal = true
		d.outcome = boundary.Outcome(result.IsAuthorized(), result.HasExceptions(), err != nil, ctx.Err() != nil, result.details.ValidationResults)
	}
}

func (o *Observation) consumptionFinished(ctx context.Context, outcome observability.Outcome, err error) {
	if o.diagnostic == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if d := o.diagnostic; d != nil {
		if outcome == observability.Success && err != nil {
			outcome = observability.Error
			if ctx.Err() != nil || o.ctx.Err() != nil {
				outcome = observability.Cancelled
			}
		}
		d.outcome, d.terminal = outcome, true
		d.record(observability.Consumption, outcome, d.consuming)
	}
}

func (o *Observation) acknowledged(result Result[any]) {
	if o.diagnostic == nil && o.pipeline.healthLabels == nil {
		return
	}
	if !result.IsSuccess() {
		return
	}
	if _, present := result.Data(); !present {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.delivered++
	if d := o.diagnostic; d != nil {
		if o.delivered == 1 {
			d.record(observability.FirstDelivery, observability.Success, d.started)
		}
		d.record(observability.Delivered, observability.Success, time.Time{})
	}
}

func (o *Observation) closingDiagnostics() {
	if o.diagnostic == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if d := o.diagnostic; d != nil && d.cleanup.IsZero() {
		d.cleanup = time.Now()
		// Capture this before Close's own cancellation. A consumer that is still
		// running will replace it with its actual terminal outcome before joining.
		if !d.terminal {
			d.outcome = observability.Cancelled
		}
	}
}

// closeDiagnostics refreshes health retention for any Close caller. It never
// certifies a join: a caller that timed out or lost the gate has no authority.
func (o *Observation) closeDiagnostics() {
	if o.diagnostic == nil && o.pipeline.healthLabels == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.retained = !o.closed
}

// markClosed is called only by the serialized closer after all ownership was
// released. The stored cleanup result classifies the single Joined/Cleanup pair.
func (o *Observation) markClosed() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed, o.retained = true, false
	if d := o.diagnostic; d != nil && !o.diagnosticJoined {
		o.diagnosticJoined = true
		outcome := d.outcome
		if o.closeErr != nil && outcome != observability.Authorization {
			outcome = observability.Error
		}
		d.record(observability.Joined, outcome, d.started)
		d.record(observability.Cleanup, outcome, d.cleanup)
	}
}
