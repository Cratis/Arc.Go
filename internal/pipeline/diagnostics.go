// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/validation"
)

type diagnosticsKey struct{}

// DiagnosticSource is optional; custom pipelines need not implement it.
type DiagnosticSource interface {
	Diagnostics() *observability.Recorder
}

// Attempt owns exactly one logical invocation across first-party dispatch layers.
// It holds only bounded labels and a private monotonic timer, not request data.
type Attempt struct {
	recorder          *observability.Recorder
	event             observability.Observation
	start             time.Time
	parent            *Attempt
	cancelled         atomic.Bool
	forwarded         atomic.Uint32
	finished          atomic.Bool
	completeOnForward bool
}

// Begin forwards an existing logical attempt or starts an independent one.
// Inner layers forward only scalar cancellation evidence to the outer owner.
func Begin(ctx context.Context, source any, operation observability.Operation, name string, transport observability.Transport, phase observability.Phase) (context.Context, *Attempt) {
	capability, ok := source.(DiagnosticSource)
	if !ok {
		return ctx, nil
	}
	recorder := capability.Diagnostics()
	if recorder == nil {
		return ctx, nil
	}
	if ctx != nil {
		if previous, ok := ctx.Value(diagnosticsKey{}).(*Attempt); ok && previous != nil && previous.recorder == recorder && previous.event.Operation == operation {
			return ctx, &Attempt{parent: previous}
		}
	}
	attempt := &Attempt{recorder: recorder, event: observability.Observation{Artifact: recorder.Label(name), Operation: operation, Transport: transport, Phase: phase}, start: time.Now()}
	if ctx != nil {
		ctx = context.WithValue(ctx, diagnosticsKey{}, attempt)
	}
	return ctx, attempt
}

// ForwardDiagnostics passes an ingress-owned attempt only to framework dispatch.
// The finalized backend result completes it before transport consumption/publication.
func ForwardDiagnostics(ctx context.Context, attempt *Attempt) context.Context {
	if ctx == nil || attempt == nil {
		return ctx
	}
	attempt.completeOnForward = true
	return context.WithValue(ctx, diagnosticsKey{}, attempt)
}

// FinishForwarded prefers the finalized backend outcome when dispatch occurred.
func (a *Attempt) FinishForwarded(fallback observability.Outcome) {
	if a == nil {
		return
	}
	owner := a
	if a.parent != nil {
		owner = a.parent
	}
	if outcome := owner.forwarded.Load(); outcome != 0 {
		fallback = observability.Outcome(outcome - 1)
	}
	a.Finish(fallback)
}

// ClearDiagnostics prevents application callbacks from inheriting dispatch tokens.
func ClearDiagnostics(ctx context.Context) context.Context {
	if ctx == nil || ctx.Value(diagnosticsKey{}) == nil {
		return ctx
	}
	return context.WithValue(ctx, diagnosticsKey{}, (*Attempt)(nil))
}

// Finish records scalars only; it never inspects error values or application data.
func (a *Attempt) Finish(outcome observability.Outcome) {
	if a == nil {
		return
	}
	if a.finished.Swap(true) {
		return
	}
	if a.parent != nil {
		a.parent.forwarded.Store(uint32(outcome) + 1)
		if outcome == observability.Cancelled {
			a.parent.cancelled.Store(true)
		}
		if a.parent.completeOnForward {
			a.parent.Finish(outcome)
		}
		return
	}
	if outcome == observability.Error && a.cancelled.Load() {
		outcome = observability.Cancelled
	}
	event := a.event
	event.Outcome = outcome
	event.Seconds = time.Since(a.start).Seconds()
	a.recorder.Record(event)
}

// Outcome preserves reference precedence using finalized flags and reason enums.
// The error itself is deliberately absent: no Error, Is, Unwrap or reflection.
func Outcome(authorized, exceptions, failed, cancelled bool, findings []validation.Result) observability.Outcome {
	if !authorized {
		return observability.Authorization
	}
	if exceptions {
		if cancelled {
			return observability.Cancelled
		}
		return observability.Error
	}
	if len(findings) > 0 {
		for _, finding := range findings {
			if finding.Reason == validation.ConstraintViolation || finding.Reason == validation.ConcurrencyViolation {
				return observability.AppendRejected
			}
		}
		return observability.Validation
	}
	if failed {
		if cancelled {
			return observability.Cancelled
		}
		return observability.Error
	}
	return observability.Success
}
