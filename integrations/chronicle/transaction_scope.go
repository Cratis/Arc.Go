// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/cratis/arc.go/commands"
)

type transaction struct {
	mu           sync.Mutex
	busy, closed bool
	bound        bool
	unsubscribe  func()
	immediate    commands.CompletionReport
	coordinates  Coordinates
	actor        Actor
	participant  Participant
	owner        CompletionOwner
	poisoned     error
	result       CommitResult
	failure      error
	scopes       map[string]LabeledScope
	aggregates   map[any]any
}

// Metadata carries copied audit facts only; no transaction or completion capability.
type Metadata struct {
	Actor  Actor
	Causes []Cause
	// Expected constrains reactor-origin commands to the mapped store/namespace.
	Expected *Coordinates
}
type metadataKey struct{}

func WithMetadata(ctx context.Context, value Metadata) context.Context {
	value.Causes = cloneCauses(value.Causes)
	if value.Expected != nil {
		copy := *value.Expected
		value.Expected = &copy
	}
	return context.WithValue(ctx, metadataKey{}, value)
}
func MetadataFrom(ctx context.Context) Metadata {
	value, _ := ctx.Value(metadataKey{}).(Metadata)
	value.Causes = cloneCauses(value.Causes)
	if value.Expected != nil {
		copy := *value.Expected
		value.Expected = &copy
	}
	return value
}
func (frame *commandFrame) context(ctx context.Context) context.Context {
	return WithMetadata(ctx, Metadata{Actor: frame.actor, Causes: frame.causes})
}

type terminalScope struct{ integration *Integration }

func (s terminalScope) Begin(ctx context.Context, inv *commands.Invocation) error {
	return s.integration.begin(ctx, inv)
}
func (s terminalScope) Complete(ctx context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
	return s.integration.complete(ctx, inv, result)
}

func (i *Integration) begin(ctx context.Context, inv *commands.Invocation) error {
	return commands.SetRootState(ctx, inv, i.root, &transaction{scopes: map[string]LabeledScope{}, aggregates: map[any]any{}})
}
func (i *Integration) transaction(ctx context.Context, inv *commands.Invocation) (*transaction, error) {
	if inv == nil || inv.CommandContext().IsValidationOnly() {
		return nil, commands.ErrExecutionMismatch
	}
	tx, found, err := commands.RootState(ctx, inv, i.root)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, commands.ErrNoContext
	}
	return tx, nil
}
func (tx *transaction) enter() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return ErrClosed
	}
	if tx.busy {
		return ErrConcurrent
	}
	tx.busy = true
	return nil
}
func (tx *transaction) leave() { tx.mu.Lock(); tx.busy = false; tx.mu.Unlock() }
func (tx *transaction) poison(err error) {
	if err != nil {
		tx.mu.Lock()
		tx.poisoned = errors.Join(tx.poisoned, err)
		tx.mu.Unlock()
	}
}
func (i *Integration) stage(ctx context.Context, inv *commands.Invocation, batch Batch) (err error) {
	tx, err := i.transaction(ctx, inv)
	if err != nil {
		return err
	}
	if err = tx.enter(); err != nil {
		tx.poison(err)
		return err
	}
	defer tx.leave()
	defer func() { tx.poison(err) }()
	frame, err := i.frameFor(ctx, inv)
	if err != nil {
		return err
	}
	if tx.bound && (tx.coordinates != frame.coordinates || tx.actor != frame.actor) {
		return ErrMismatch
	}
	// Preflight the complete enrollment before connection, scope reads or staging.
	for _, entry := range batch.Entries {
		if err = i.preflight(EventValue{entry: entry, explicit: true}); err != nil {
			return err
		}
	}
	for index, scope := range batch.Scopes {
		if err = validateScope(scope); err != nil {
			return err
		}
		if scope.Expectation.Kind == Resolve {
			if isNil(i.options.Concurrency) {
				return ErrUnsupported
			}
			resolved, resolveErr := i.options.Concurrency.ResolveScope(frame.context(ctx), ScopeRequest{Coordinates: frame.coordinates, Filter: scope.Filter})
			if resolveErr != nil {
				return resolveErr
			}
			resolved.Label = scope.Label
			batch.Scopes[index] = resolved
		}
	}
	if len(batch.Entries) == 0 && len(batch.Scopes) == 0 {
		return nil
	}
	if frame.options.ConcurrencySourceType || frame.options.ConcurrencyStreamType || frame.options.ConcurrencyStreamID {
		if isNil(i.options.Concurrency) {
			return ErrUnsupported
		}
		for _, entry := range batch.Entries {
			explicit := false
			for _, scope := range batch.Scopes {
				if scope.Label == string(entry.Source) {
					explicit = true
					break
				}
			}
			if explicit {
				continue
			}
			filter := Filter{Source: entry.Source}
			if frame.options.ConcurrencySourceType {
				filter.Route.SourceType = entry.Route.SourceType
			}
			if frame.options.ConcurrencyStreamType {
				filter.Route.StreamType = entry.Route.StreamType
			}
			if frame.options.ConcurrencyStreamID {
				filter.Route.StreamID = entry.Route.StreamID
			}
			cacheKey := string(entry.Source) + "\x00" + filter.Route.SourceType + "\x00" + filter.Route.StreamType + "\x00" + filter.Route.StreamID
			scope, found := frame.scopes[cacheKey]
			if !found {
				scope, err = i.options.Concurrency.ResolveScope(frame.context(ctx), ScopeRequest{Coordinates: frame.coordinates, Filter: filter})
				if err != nil {
					return err
				}
				frame.scopes[cacheKey] = scope
			}
			batch.Scopes = append(batch.Scopes, scope)
		}
	}
	// Keep only scope diagnostics, never event payloads. Repeated identical checks
	// are enrolled once; incompatible source boundaries fail before any append.
	unique := map[string]LabeledScope{}
	var scopes []LabeledScope
	for _, scope := range batch.Scopes {
		if err = validateScope(scope); err != nil {
			return err
		}
		if prior, found := tx.scopes[scope.Label]; found {
			if !reflect.DeepEqual(prior, scope) {
				return ErrMismatch
			}
			continue
		}
		if prior, found := unique[scope.Label]; found {
			if !reflect.DeepEqual(prior, scope) {
				return ErrMismatch
			}
			continue
		}
		unique[scope.Label] = scope
		scopes = append(scopes, scope)
	}
	batch.Scopes = scopes
	if tx.owner == nil {
		tx.participant, tx.owner, err = i.options.Transactions.Begin(frame.context(ctx), frame.coordinates)
		if err != nil {
			return err
		}
		if isNil(tx.participant) || isNil(tx.owner) {
			return ErrInvalid
		}
		tx.mu.Lock()
		tx.coordinates, tx.actor, tx.bound = frame.coordinates, frame.actor, true
		tx.mu.Unlock()
	}
	if err = inv.Execution().Check(ctx); err != nil {
		return err
	}
	if err = tx.participant.Stage(frame.context(ctx), batch); err != nil {
		return err
	}
	for label, scope := range unique {
		tx.scopes[label] = scope
	}
	return nil
}
func (i *Integration) complete(ctx context.Context, inv *commands.Invocation, result commands.Result[any]) (commands.CompletionReport, error) {
	tx, err := i.transaction(ctx, inv)
	if err != nil {
		return commands.CompletionReport{}, err
	}
	return i.finish(ctx, inv, tx, result.IsSuccess())
}
func (i *Integration) finish(ctx context.Context, inv *commands.Invocation, tx *transaction, success bool) (commands.CompletionReport, error) {
	tx.mu.Lock()
	if tx.closed {
		report, err := tx.result.Report, errors.Join(tx.failure, tx.poisoned)
		tx.mu.Unlock()
		return report, err
	}
	if tx.busy {
		tx.mu.Unlock()
		return commands.CompletionReport{Disposition: commands.OutcomeUnknown}, ErrConcurrent
	}
	tx.closed = true
	tx.result.Report.Disposition = commands.OutcomeUnknown
	stop := tx.unsubscribe
	tx.unsubscribe = nil
	tx.mu.Unlock()
	if stop != nil {
		stop()
	}
	tx.mu.Lock()
	poison, immediate := tx.poisoned, tx.immediate
	tx.mu.Unlock()
	result := CommitResult{}
	var err error
	if !success || poison != nil {
		result.Report.Disposition = commands.NotCommitted
		if tx.owner != nil {
			err = tx.owner.Rollback()
		}
	} else if tx.owner != nil {
		frame, frameErr := i.frameFor(ctx, inv)
		if frameErr != nil {
			result.Report.Disposition = commands.NotCommitted
			err = errors.Join(frameErr, tx.owner.Rollback())
		} else {
			// The factory/SDK owns the only production event buffer and single commit.
			result, err = tx.owner.Commit(frame.context(ctx))
		}
	}
	failure := errors.Join(poison, err)
	if success && poison == nil {
		failure = result.Failure(inv.CommandContext().Command(), err)
	}
	result.Report = mergeObserved(result.Report, immediate)
	if logger := i.options.Logger; logger != nil && (result.Report.Disposition == commands.OutcomeUnknown || result.Report.Disposition == commands.MixedCommit) {
		logger.ErrorContext(ctx, "Chronicle outcome unknown; reconcile before resubmission", "correlationId", inv.CommandContext().CorrelationID(), "store", tx.coordinates.Store, "namespace", tx.coordinates.Namespace, "sequence", tx.coordinates.Sequence)
	}
	tx.mu.Lock()
	tx.result, tx.failure = result, failure
	tx.mu.Unlock()
	return result.Report, failure
}
