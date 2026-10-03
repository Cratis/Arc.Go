// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/cratis/arc.go/execution"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/serialization"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// RecoveryStatus describes observed callback recovery, never atomic rollback.
type RecoveryStatus uint8

const (
	// RecoveryNotNeeded means no failed started work requires reversal.
	RecoveryNotNeeded RecoveryStatus = iota
	// RecoveryCompleted means every eligible compensator returned successfully.
	RecoveryCompleted
	// RecoveryIncomplete means one or more reversals were unavailable or failed.
	RecoveryIncomplete
	// RecoverySuppressed means confirmed persistence prohibits automatic reversal.
	RecoverySuppressed
	// RecoveryIndeterminate means unknown/mixed persistence prohibits reversal.
	RecoveryIndeterminate
)

// OperationCompensation describes one started invocation's recovery observation.
type OperationCompensation uint8

const (
	// CompensationNotNeeded means recovery was not required.
	CompensationNotNeeded OperationCompensation = iota
	// CompensationCompleted means the callback returned successfully.
	CompensationCompleted
	// CompensationFailed means the callback failed, panicked or exceeded its budget.
	CompensationFailed
	// CompensationNotAvailable means no reversal was declared.
	CompensationNotAvailable
	// CompensationBudgetExpired means the callback was not entered before expiry.
	CompensationBudgetExpired
	// CompensationSuppressed means persistence facts prohibited reversal.
	CompensationSuppressed
)

// OperationOutcome is a backend-only observation with no declaration payload.
// Error objects remain borrowed and must not be mutated after returning.
type OperationOutcome struct {
	InvocationIndex     int
	OperationType       reflect.Type
	ExecutionCompleted  bool
	Compensation        OperationCompensation
	CompensationFailure error
}

// RecoverySummary is a backend-only immutable value snapshot. Completion() on the
// Result remains the sole persistence report. Completed is not a retry guarantee.
type RecoverySummary struct {
	Status                                         RecoveryStatus
	StartedCount, CompletedCount, CompensatedCount int
	FailedCompensationCount, UncompensatedCount    int
}

type operationObservations struct {
	summary  RecoverySummary
	outcomes []OperationOutcome
}

// Recovery returns backend-only recovery observations, if processing participated.
func (r Result[R]) Recovery() (RecoverySummary, bool) {
	if r.details.operations == nil {
		return RecoverySummary{}, false
	}
	return r.details.operations.summary, true
}

// OperationOutcomes returns copied backend-only observations, never payloads.
func (r Result[R]) OperationOutcomes() []OperationOutcome {
	if r.details.operations == nil {
		return nil
	}
	return append([]OperationOutcome(nil), r.details.operations.outcomes...)
}

// AddOperationExecutionScope explicitly classifies a noncommitting scope. Begin
// must not commit; Complete must not persist business changes. Recovery services
// remain usable until the originating resource owner disposes them. This promise
// is static: Arc never activates factories merely to discover compatibility.
func (r *Registry) AddOperationExecutionScope(name string, factory Factory[ExecutionScope], keys ...di.Key) error {
	if err := r.AddExecutionScope(name, factory, keys...); err != nil {
		return err
	}
	r.participants[len(r.participants)-1].operations = true
	return nil
}

// OperationCommitParticipant is the existing sole terminal slot plus a read-only
// observation hook. ObserveCommit reports provider facts before operation entry;
// it must not connect, commit, retry or activate business services. Pending work
// can report NotCommitted. Complete owns the authoritative final outcome.
// Recovery dependencies must remain usable after completion; a completed deferred
// transaction itself is not a usable recovery resource.
type OperationCommitParticipant interface {
	DeferredCommitParticipant
	ObserveCommit(context.Context, *Invocation) (CompletionReport, error)
}

// AddOperationCommitParticipant explicitly classifies the sole terminal slot.
// Unclassified terminal participants remain supported for non-operation commands.
func (r *Registry) AddOperationCommitParticipant(name string, factory Factory[OperationCommitParticipant], keys ...di.Key) error {
	if factory == nil {
		return ErrInvalidRegistration
	}
	if err := r.AddDeferredCommitParticipant(name, func(ctx context.Context, scope *execution.Scope) (DeferredCommitParticipant, error) {
		value, err := factory(ctx, scope)
		if err == nil && nilValue(value) {
			err = ErrInvalidRegistration
		}
		return value, err
	}, keys...); err != nil {
		return err
	}
	r.terminal[len(r.terminal)-1].operations = true
	return nil
}

// CheckExplicitCommit refuses early persistence in an operation-capable frame.
// Refusal is sticky even when ignored. It observes existing callback/security
// ownership; it grants neither authorization nor commit authority otherwise.
func (e *Execution) CheckExplicitCommit(ctx context.Context) error {
	if e == nil {
		return ErrNoContext
	}
	return withState(ctx, e.invocation, func(current *Execution) error {
		if current.frame.registration.operations {
			current.state.operationAttempts++
			return ErrInvalidOperation
		}
		return nil
	})
}

func operationScopesCompatible(participants []extension[ExecutionScope], terminal []extension[DeferredCommitParticipant]) bool {
	for _, scope := range participants {
		if !scope.operations {
			return false
		}
	}
	for _, scope := range terminal {
		if !scope.operations {
			return false
		}
	}
	return true
}

type operationJournal struct {
	planned           []operationCall
	outcomes          []OperationOutcome
	original          Result[NoResponse]
	cause             error
	source            OperationFailureSource
	failedIndex       int
	failed, recovered bool
	summary           RecoverySummary
}

func (f *frame) captureOperationFailure() {
	journal := f.operations
	if journal == nil || journal.failed || f.result.IsSuccess() {
		return
	}
	journal.failed = true
	journal.original = NewResult(f.result.Details(), serialization.Optional[NoResponse]{})
	journal.cause, journal.source = f.err, f.operationPhase
	if (f.operationPhase != FailureScopeCompletion || f.ctx.Err() != nil) && (errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded)) {
		journal.source = FailureCancellation
	}
}

// planOperations validates every declaration before resolving any bundle.
func (f *frame) planOperations(leaves []outcomeLeaf) ([]outcomeLeaf, error) {
	var values []Operation
	ordinary := make([]outcomeLeaf, 0, len(leaves))
	for _, leaf := range leaves {
		switch value := leaf.value.(type) {
		case Operation:
			values = append(values, value)
		case Operations:
			values = append(values, value.values...)
		default:
			ordinary = append(ordinary, leaf)
		}
	}
	for _, value := range values {
		if nilValue(value) {
			return nil, ErrInvalidOperation
		}
		if _, ok := f.pipeline.operations[reflect.TypeOf(value)]; !ok {
			return nil, ErrInvalidOperation
		}
	}
	for _, value := range values {
		adapter := f.pipeline.operations[reflect.TypeOf(value)]
		var call operationCall
		err := f.call(func(ctx context.Context, inv *Invocation) error {
			var err error
			call, err = adapter.prepare(ctx, inv.Scope(), value)
			return err
		})
		if err != nil {
			return nil, err
		}
		f.operations.planned = append(f.operations.planned, call)
	}
	return ordinary, nil
}

func (f *frame) checkOperationCommit() error {
	report := f.completionReport()
	if f.operationCommit != nil {
		var observed CompletionReport
		err := f.call(func(ctx context.Context, inv *Invocation) error {
			var err error
			observed, err = f.operationCommit.ObserveCommit(ctx, inv)
			return err
		})
		if observed.Disposition > MixedCommit || err != nil {
			observed.Disposition = OutcomeUnknown
		}
		// Pending/no-work pre-entry observations do not describe a completed
		// attempt. Only hazardous facts become sticky persistence evidence.
		if observed.Disposition == Committed || observed.Disposition == OutcomeUnknown || observed.Disposition == MixedCommit {
			f.owner.mu.Lock()
			f.owner.report = mergeCompletion(f.owner.report, observed)
			f.owner.reports++
			f.owner.mu.Unlock()
		}
		report = f.completionReport()
		if err != nil {
			return errors.Join(ErrInvalidOperation, err)
		}
	}
	if report.Disposition == Committed || report.Disposition == OutcomeUnknown || report.Disposition == MixedCommit {
		return ErrInvalidOperation
	}
	return nil
}

func (f *frame) executeOperations() {
	f.operationPhase = FailureExecution
	if err := f.checkOperationCommit(); err != nil {
		f.fail(err, false)
		return
	}
	for index, call := range f.operations.planned {
		err := f.call(func(ctx context.Context, _ *Invocation) error {
			if err := f.prepared.Check(ctx); err != nil {
				return err
			}
			// Scope.Use and boundary.Call admitted cancellation/security before
			// this point. No journal entry exists for a refused callback.
			f.operations.outcomes = append(f.operations.outcomes, OperationOutcome{InvocationIndex: index, OperationType: call.typ})
			f.operations.failedIndex = index // A recovered panic is an entered failure too.
			err := call.execute(ctx)
			if err == nil {
				f.operations.outcomes[len(f.operations.outcomes)-1].ExecutionCompleted = true
				f.operations.failedIndex = -1
			}
			return err
		})
		f.fail(err, false)
		if err != nil || !f.result.IsSuccess() {
			return
		}
	}
}

func (f *frame) recoverOperations() {
	journal := f.operations
	if journal == nil || journal.recovered {
		return
	}
	journal.recovered = true // Disposal/join resumption never reenters recovery.
	f.captureOperationFailure()
	requires := journal.failed && len(journal.outcomes) != 0
	status := RecoveryNotNeeded
	report := f.completionReport()
	if requires && report.Disposition != NoPersistedWork && report.Disposition != NotCommitted {
		status = RecoveryIndeterminate
		if report.Disposition == Committed {
			status = RecoverySuppressed
		}
		for index := range journal.outcomes {
			journal.outcomes[index].Compensation = CompensationSuppressed
		}
	} else if requires {
		timeout := f.pipeline.options.Operations.CompensationTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		// Preserve principal/tenant PRESENCE and receipt/correlation metadata,
		// but acquire a fresh budget after ordinary and terminal completion.
		cleanup, cancel, err := boundary.CleanupContext(f.ctx, timeout)
		if cancel != nil {
			defer cancel()
		}
		for index := len(journal.outcomes) - 1; index >= 0; index-- {
			outcome := &journal.outcomes[index]
			call := journal.planned[outcome.InvocationIndex]
			if call.compensate == nil {
				outcome.Compensation = CompensationNotAvailable
				continue
			}
			if err != nil || cleanup.Err() != nil {
				outcome.Compensation = CompensationBudgetExpired
				continue
			}
			failure := OperationFailure{InvocationIndex: outcome.InvocationIndex, InvocationCompleted: outcome.ExecutionCompleted, IsFailingInvocation: journal.failedIndex == outcome.InvocationIndex, Source: journal.source, Completion: report, Original: journal.original, Cause: journal.cause}
			recoveryErr := f.callWith(cleanup, func(ctx context.Context, _ *Invocation) error { return call.compensate(ctx, failure) })
			outcome.Compensation = CompensationCompleted
			if recoveryErr != nil {
				outcome.Compensation, outcome.CompensationFailure = CompensationFailed, recoveryErr
			}
		}
		status = RecoveryCompleted
		for _, outcome := range journal.outcomes {
			if outcome.Compensation != CompensationCompleted {
				status = RecoveryIncomplete
				break
			}
		}
	}
	journal.summary = RecoverySummary{Status: status, StartedCount: len(journal.outcomes)}
	for _, outcome := range journal.outcomes {
		if outcome.ExecutionCompleted {
			journal.summary.CompletedCount++
		}
		if outcome.Compensation == CompensationCompleted {
			journal.summary.CompensatedCount++
		}
		if outcome.Compensation == CompensationFailed {
			journal.summary.FailedCompensationCount++
		}
	}
	if requires {
		journal.summary.UncompensatedCount = len(journal.outcomes) - journal.summary.CompensatedCount
	}
	// Release borrowed declarations/dependencies after joined recovery. Backend
	// snapshots contain only types, counts and error observations.
	journal.planned = nil
}

func operationBoundaryFor(ctx context.Context, p Pipeline) *boundPipeline {
	if bound, ok := p.(*boundPipeline); ok {
		return bound
	}
	if ctx != nil {
		if core, ok := p.(*pipeline); ok {
			if bound, ok := ctx.Value(callbackBoundaryKey{}).(*boundPipeline); ok && bound.pipeline == core {
				return bound
			}
		}
	}
	return nil
}

// callbackBoundaryKey carries private, expiring same-host admission metadata.
// It is not a resolver/service-locator capability for applications.
type callbackBoundaryKey struct{}
