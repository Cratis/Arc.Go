// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"errors"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/integrations/chronicle/internal/appendorigin"
)

// AppendObserver observes dispatched immediate appends on the selected cached
// sequence, attributed to this execution's exact nonzero provider-owned origin.
// Subscribe runs after authorization. The returned function must stop admissions
// and join callbacks. Before-dispatch errors and unrelated low-level handles are
// not observable: direct SDK callers must still check every append result.
// Correlation is diagnostic only. Unit-owner commits have their own origin.
type AppendObserver interface {
	Subscribe(context.Context, Coordinates, correlation.ID, func(CommitResult, error)) (func(), error)
}

// OnExecution establishes command-attributable immediate-append observation after
// authorization, never during advisory validation. It may establish store readiness.
//
// Operation-capable commands begin the Chronicle transaction after filters and
// validation. Their root filter therefore only records the authorized request;
// the terminal Begin opens the transaction, publishes the origin and subscribes
// before Provide/Handle or operations run. The integration never persists during
// filters or validation; direct application appends before Begin are unattributed.
func (i *Integration) OnExecution(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
	success := commands.Success(inv.CommandContext().CorrelationID())
	if inv.CommandContext().IsValidationOnly() {
		return success, nil
	}
	frame, err := i.frameFor(ctx, inv)
	if err != nil {
		return success, err
	}
	tx, found, err := commands.RootState(ctx, inv, i.root)
	if err != nil {
		return success, err
	}
	if !found {
		// Only an operation-capable root frame reaches its filters before Begin.
		if !inv.Execution().IsRoot() {
			return success, commands.ErrNoContext
		}
		frame.observation = true
		return success, nil
	}
	return success, i.observe(ctx, inv, tx, frame)
}

// observe binds the root transaction to this frame and subscribes once.
func (i *Integration) observe(ctx context.Context, inv *commands.Invocation, tx *transaction, frame *commandFrame) error {
	// Every authorized frame publishes the immutable root token, including joined
	// children whose root is already bound. SetValue updates subsequent callbacks.
	if tx.origin != nil {
		if err := inv.SetValue(appendorigin.Name, tx.origin); err != nil {
			return err
		}
	}
	tx.mu.Lock()
	if tx.bound && (tx.coordinates != frame.coordinates || tx.actor != frame.actor) {
		tx.mu.Unlock()
		return ErrMismatch
	}
	if tx.bound {
		tx.mu.Unlock()
		return nil
	}
	tx.coordinates, tx.actor, tx.bound = frame.coordinates, frame.actor, true
	tx.mu.Unlock()
	command := inv.CommandContext().Command()
	// The adapter decorates this current filter context locally with WithOrigin;
	// its snapshot predates SetValue. No callback context is replaced or retained.
	ctx = appendorigin.WithToken(frame.context(ctx), tx.origin)
	stop, err := i.options.Appends.Subscribe(ctx, frame.coordinates, inv.CommandContext().CorrelationID(), func(result CommitResult, operation error) {
		failure := result.Failure(command, operation)
		tx.mu.Lock()
		tx.immediate = mergeObserved(tx.immediate, result.Report)
		tx.poisoned = errors.Join(tx.poisoned, failure)
		tx.mu.Unlock()
	})
	if err != nil {
		return err
	}
	if stop == nil {
		return ErrInvalid
	}
	tx.mu.Lock()
	tx.unsubscribe, tx.observed = stop, true
	tx.mu.Unlock()
	return nil
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
