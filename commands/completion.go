// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"

	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// CommitDisposition describes observed persistence, not command success. A failed
// command can have committed. No disposition authorizes an automatic retry.
type CommitDisposition uint8

const (
	// NoPersistedWork means no events/writes persisted (including successful checks only).
	NoPersistedWork CommitDisposition = iota
	// NotCommitted means the provider established that work did not commit.
	NotCommitted
	// Committed means the provider confirmed persistence.
	Committed
	// OutcomeUnknown requires reconciliation; persistence may have occurred.
	OutcomeUnknown
	// MixedCommit means observations include both confirmed persistence and other outcomes.
	MixedCommit
)

// CompletionReport is server-side evidence retained independently of the HTTP
// envelope and command success. Zero means no persisted work. MixedCommit can
// represent immediate and deferred effects; Arc does not coordinate providers.
type CompletionReport struct {
	// Disposition is the provider's observed outcome, never inferred from IsSuccess.
	Disposition CommitDisposition
}

// CompletionError preserves a failed operation's cause and persistence report,
// including failures after confirmed persistence. Unwrap retains errors.Is/As.
type CompletionError struct {
	// Report records persistence independently of Cause.
	Report CompletionReport
	// Cause is the original failure, possibly a joined error.
	Cause error
}

// Error describes the failure without claiming a rollback.
func (e *CompletionError) Error() string { return "command completion: " + e.Cause.Error() }

// Unwrap exposes the original error.
func (e *CompletionError) Unwrap() error { return e.Cause }

func withCompletionError(err error, report CompletionReport) error {
	if err == nil || report.Disposition == NoPersistedWork {
		return err
	}
	return &CompletionError{Report: report, Cause: err}
}

// DeferredCommitParticipant reserves ownership before ordinary ExecutionScope
// Begin callbacks. Complete runs once, after all ordinary Complete callbacks and
// before resource disposal. It receives their merged outcome and a live bounded
// cleanup context, even after a failed Begin. Validate never activates it.
//
// Begin must not connect or activate tenant resources before authorization; use
// lazy binding in the provider. Complete must roll back open work on failure,
// report the provider's actual disposition even when returning an error, and never
// retry an ambiguous commit. Domain rejections use validation-bearing errors.
// Use ReportCommit for early completion so later callback failures retain evidence.
type DeferredCommitParticipant interface {
	Begin(context.Context, *Invocation) error
	Complete(context.Context, *Invocation, Result[any]) (CompletionReport, error)
}

// AddDeferredCommitParticipant registers the sole terminal participant. A second
// registration is rejected irrespective of its name; cross-provider atomicity is
// not supported. The borrowed factory is lazy, and Build performs no I/O.
func (r *Registry) AddDeferredCommitParticipant(name string, factory Factory[DeferredCommitParticipant], keys ...di.Key) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	if len(r.terminal) != 0 {
		return ErrDuplicate
	}
	return addExtension(r, "terminal", name, factory, keys, &r.terminal)
}

// ReportCommit retains an early persistence observation on the command owner.
// It checks callback/security continuity and refuses validation-only execution.
// Later failures or no-work completion cannot erase confirmed/unknown outcomes.
// This records facts only; it neither commits nor grants commit authority.
func ReportCommit(ctx context.Context, inv *Invocation, report CompletionReport) error {
	if report.Disposition > MixedCommit {
		return ErrInvalidRegistration
	}
	return withState(ctx, inv, func(e *Execution) error {
		if e.frame.snapshot.validationOnly {
			return ErrExecutionMismatch
		}
		e.state.report = mergeCompletion(e.state.report, report)
		e.state.reports++
		return nil
	})
}

func mergeCompletion(a, b CompletionReport) CompletionReport {
	if a.Disposition == NoPersistedWork {
		return b
	}
	if b.Disposition == NoPersistedWork || a == b {
		return a
	}
	if a.Disposition == MixedCommit || b.Disposition == MixedCommit || a.Disposition == Committed || b.Disposition == Committed {
		return CompletionReport{Disposition: MixedCommit}
	}
	return CompletionReport{Disposition: OutcomeUnknown}
}

func (f *frame) completionReport() CompletionReport {
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	return f.owner.report
}

func (f *frame) completeTerminal(ctx context.Context, participant DeferredCommitParticipant) {
	f.owner.mu.Lock()
	f.owner.completing = true // No new nested command may join a finalizing owner.
	reportsBefore := f.owner.reports
	f.owner.mu.Unlock()
	var report CompletionReport
	returned := false
	err := f.callWith(ctx, func(ctx context.Context, inv *Invocation) error {
		var err error
		report, err = participant.Complete(ctx, inv, f.final())
		returned = true
		return err
	})
	if report.Disposition > MixedCommit {
		report.Disposition = OutcomeUnknown
		f.fail(ErrInvalidRegistration, false)
	}
	f.owner.mu.Lock()
	// A failed attempt without its own observation is uncertain. An observation
	// made inside this Complete survives a later panic/cancellation. Observations
	// from earlier attempts cannot establish this attempt's outcome.
	if (!returned || (err != nil && report.Disposition == NoPersistedWork)) && f.owner.reports == reportsBefore {
		report.Disposition = OutcomeUnknown
	}
	f.owner.report = mergeCompletion(f.owner.report, report)
	f.owner.reports++
	finalReport := f.owner.report
	f.owner.mu.Unlock()
	f.fail(err, false)
	if f.result.IsSuccess() && finalReport.Disposition != NoPersistedWork && finalReport.Disposition != Committed {
		f.fail(ErrCommitNotConfirmed, false)
	}
}
