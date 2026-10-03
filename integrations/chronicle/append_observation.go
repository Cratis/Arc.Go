// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"errors"
	"sync"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
)

// AppendObserver observes dispatched appends on the selected cached sequence.
// Subscribe runs after authorization. The returned function must stop admissions
// and join callbacks. Before-dispatch errors and unrelated low-level handles are
// not observable: direct SDK callers must still check every append result.
// Notifications must match the exact coordinates and correlation, including zero.
// The integration suppresses ambiguous shared-correlation observations and its
// owner-commit windows; correlation alone cannot prove execution ownership.
type AppendObserver interface {
	Subscribe(context.Context, Coordinates, correlation.ID, func(CommitResult, error)) (func(), error)
}

// OnExecution establishes command-attributable immediate-append observation after
// authorization, never during advisory validation. It may establish store readiness.
func (i *Integration) OnExecution(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
	success := commands.Success(inv.CommandContext().CorrelationID())
	if inv.CommandContext().IsValidationOnly() {
		return success, nil
	}
	tx, err := i.transaction(ctx, inv)
	if err != nil {
		return success, err
	}
	frame, err := i.frameFor(ctx, inv)
	if err != nil {
		return success, err
	}
	tx.mu.Lock()
	if tx.bound && (tx.coordinates != frame.coordinates || tx.actor != frame.actor) {
		tx.mu.Unlock()
		return success, ErrMismatch
	}
	if tx.bound {
		tx.mu.Unlock()
		return success, nil
	}
	tx.coordinates, tx.actor, tx.bound = frame.coordinates, frame.actor, true
	tx.mu.Unlock()
	observation := i.beginObservation(frame.coordinates, inv.CommandContext().CorrelationID())
	tx.mu.Lock()
	tx.observation = observation
	tx.mu.Unlock()
	command := inv.CommandContext().Command()
	stop, err := i.options.Appends.Subscribe(frame.context(ctx), frame.coordinates, inv.CommandContext().CorrelationID(), func(result CommitResult, operation error) {
		if !observation.attribute(ctx) {
			return
		}
		failure := result.Failure(command, operation)
		tx.mu.Lock()
		tx.immediate = mergeObserved(tx.immediate, result.Report)
		tx.poisoned = errors.Join(tx.poisoned, failure)
		tx.mu.Unlock()
	})
	if err != nil {
		return success, err
	}
	if stop == nil {
		return success, ErrInvalid
	}
	tx.mu.Lock()
	tx.unsubscribe = stop
	tx.mu.Unlock()
	return success, nil
}

// Keep membership until completion returns, even after unsubscribing. Otherwise
// this command's owner notification could be attributed to a remaining singleton.
// No event payloads are retained. Separate integrations and unobserved SDK callers
// are not identifiable without the execution identity requested in Chronicle.Go#47.
type observationKey struct {
	coordinates Coordinates
	correlation correlation.ID
}
type observationGroup struct {
	commands, ownerCommits int
	logged                 bool
}
type appendObservation struct {
	integration *Integration
	key         observationKey
	group       *observationGroup
	once        sync.Once
}

func (i *Integration) beginObservation(c Coordinates, id correlation.ID) *appendObservation {
	i.observationMu.Lock()
	defer i.observationMu.Unlock()
	if i.observations == nil {
		i.observations = make(map[observationKey]*observationGroup)
	}
	key := observationKey{c, id}
	group := i.observations[key]
	if group == nil {
		group = &observationGroup{}
		i.observations[key] = group
	}
	group.commands++
	return &appendObservation{integration: i, key: key, group: group}
}

func (o *appendObservation) attribute(ctx context.Context) bool {
	i := o.integration
	i.observationMu.Lock()
	ambiguous := o.group.commands != 1
	log := ambiguous && !o.group.logged
	if log {
		o.group.logged = true
	}
	attribute := !ambiguous && o.group.ownerCommits == 0
	i.observationMu.Unlock()
	if log && i.options.Logger != nil {
		i.options.Logger.DebugContext(ctx, "Skipping ambiguous Chronicle append observation", "correlationId", o.key.correlation, "store", o.key.coordinates.Store, "namespace", o.key.coordinates.Namespace, "sequence", o.key.coordinates.Sequence)
	}
	return attribute
}

func (o *appendObservation) release() {
	o.once.Do(func() {
		i := o.integration
		i.observationMu.Lock()
		defer i.observationMu.Unlock()
		o.group.commands--
		if o.group.commands == 0 {
			delete(i.observations, o.key)
		}
	})
}

func (o *appendObservation) commit(owner CompletionOwner, ctx context.Context) (CommitResult, error) {
	i := o.integration
	i.observationMu.Lock()
	o.group.ownerCommits++
	i.observationMu.Unlock()
	defer func() {
		i.observationMu.Lock()
		o.group.ownerCommits--
		i.observationMu.Unlock()
	}()
	// This excludes all matching notifications during the synchronous Commit,
	// including unrelated immediate appends racing it. Correlation is not identity.
	return owner.Commit(ctx)
}

func mergeObserved(a, b commands.CompletionReport) commands.CompletionReport {
	if a.Disposition == commands.NoPersistedWork {
		return b
	}
	if b.Disposition == commands.NoPersistedWork || a == b {
		return a
	}
	if a.Disposition == commands.MixedCommit || b.Disposition == commands.MixedCommit || a.Disposition == commands.Committed || b.Disposition == commands.Committed {
		return commands.CompletionReport{Disposition: commands.MixedCommit}
	}
	return commands.CompletionReport{Disposition: commands.OutcomeUnknown}
}
