// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"maps"
	"strconv"

	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/constraints"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/metadata"
	"github.com/cratis/chronicle.go/transactions"
)

type participant struct {
	unit        *transactions.UnitOfWork
	coordinates integration.Coordinates
}
type completion struct {
	owner *transactions.Owner
	unit  *transactions.UnitOfWork
}

func (a *adapter) Begin(ctx context.Context, c integration.Coordinates) (integration.Participant, integration.CompletionOwner, error) {
	sequence, err := a.sequence(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	unit, owner, err := transactions.Begin(auditContext(ctx), sequence)
	if err != nil {
		return nil, nil, err
	}
	return &participant{unit: unit, coordinates: c}, &completion{owner: owner, unit: unit}, nil
}
func (p *participant) Stage(ctx context.Context, b integration.Batch) error {
	entries := make([]eventsequences.Entry, len(b.Entries))
	for index, e := range b.Entries {
		value := eventsequences.Entry{Source: events.SourceID(e.Source), Event: e.Event, Route: eventsequences.Route{SourceType: events.SourceType(e.Route.SourceType), StreamType: events.StreamType(e.Route.StreamType), StreamID: events.StreamID(e.Route.StreamID)}, Occurred: e.Occurred}
		if e.Subject != nil {
			subject := events.Subject(*e.Subject)
			value.Subject = &subject
		}
		for _, tag := range e.Tags {
			value.Tags = append(value.Tags, events.Tag(tag))
		}
		for _, tag := range e.NamedTags {
			value.NamedTags = append(value.NamedTags, events.NamedTag{Name: tag.Name, Value: tag.Value})
		}
		for _, cause := range e.Causation {
			value.Causation = append(value.Causation, metadata.Causation{Occurred: cause.Occurred, Type: cause.Type, Properties: cause.Properties})
		}
		entries[index] = value
	}
	scopes := make([]eventsequences.LabeledScope, len(b.Scopes))
	for index, value := range b.Scopes {
		scope, err := toScope(value, p.coordinates)
		if err != nil {
			return err
		}
		scopes[index] = eventsequences.LabeledScope{Label: value.Label, Scope: scope}
	}
	return p.unit.Stage(auditContext(ctx), entries, scopes...)
}
func (o *completion) Commit(ctx context.Context) (integration.CommitResult, error) {
	result, err := o.owner.Commit(auditContext(ctx))
	return mapResult(result), err
}
func mapResult(result eventsequences.BatchResult) integration.CommitResult {
	mapped := integration.CommitResult{Report: commands.CompletionReport{Disposition: commands.OutcomeUnknown}}
	switch result.Disposition {
	case eventsequences.Committed:
		mapped.Report.Disposition = commands.Committed
		if len(result.Positions) == 0 {
			mapped.Report.Disposition = commands.NoPersistedWork
		}
	case eventsequences.Rejected:
		mapped.Report.Disposition = commands.NotCommitted
	}
	for _, p := range result.Positions {
		mapped.Positions = append(mapped.Positions, uint64(p))
	}
	for _, v := range result.ConstraintViolations {
		mapped.Constraints = append(mapped.Constraints, integration.ConstraintViolation{Name: v.ConstraintName, Message: v.Message, Property: v.Details[constraints.PropertyName], Type: strconv.Itoa(int(v.Type)), Details: maps.Clone(v.Details)})
	}
	for _, v := range result.ConcurrencyViolations {
		mapped.Concurrency = append(mapped.Concurrency, integration.ConcurrencyViolation{Source: integration.EventSourceID(v.SourceID), Expected: uint64(v.Expected), Actual: uint64(v.Actual)})
	}
	for _, failure := range result.Errors {
		mapped.Errors = append(mapped.Errors, failure)
	}
	return mapped
}
func (o *completion) Rollback() error { return o.owner.Rollback() }

type resolvedScope struct {
	scope       eventsequences.Scope
	coordinates integration.Coordinates
}

func (a *adapter) ResolveScope(ctx context.Context, r integration.ScopeRequest) (integration.LabeledScope, error) {
	sequence, err := a.sequence(ctx, r.Coordinates)
	if err != nil {
		return integration.LabeledScope{}, err
	}
	scope, err := sequence.ResolveScope(auditContext(ctx), toFilter(r.Filter))
	if err != nil {
		return integration.LabeledScope{}, err
	}
	return integration.LabeledScope{Label: string(r.Filter.Source), Filter: fromFilter(scope.Filter), Expectation: integration.Expectation{Kind: integration.ProviderResolved, Token: resolvedScope{scope: scope, coordinates: r.Coordinates}}}, nil
}
func toFilter(value integration.Filter) eventsequences.ScopeFilter {
	f := eventsequences.ScopeFilter{}
	if value.Source != "" {
		v := events.SourceID(value.Source)
		f.SourceID = &v
	}
	if value.Route.SourceType != "" {
		v := events.SourceType(value.Route.SourceType)
		f.SourceType = &v
	}
	if value.Route.StreamType != "" {
		v := events.StreamType(value.Route.StreamType)
		f.StreamType = &v
	}
	if value.Route.StreamID != "" {
		v := events.StreamID(value.Route.StreamID)
		f.StreamID = &v
	}
	for _, v := range value.Events {
		f.EventTypes = append(f.EventTypes, events.TypeRef{ID: events.TypeID(v.ID), Generation: events.Generation(v.Generation)})
	}
	return f
}
func fromFilter(value eventsequences.ScopeFilter) integration.Filter {
	f := integration.Filter{}
	if value.SourceID != nil {
		f.Source = integration.EventSourceID(*value.SourceID)
	}
	if value.SourceType != nil {
		f.Route.SourceType = string(*value.SourceType)
	}
	if value.StreamType != nil {
		f.Route.StreamType = string(*value.StreamType)
	}
	if value.StreamID != nil {
		f.Route.StreamID = string(*value.StreamID)
	}
	for _, v := range value.EventTypes {
		f.Events = append(f.Events, integration.EventType{ID: string(v.ID), Generation: uint32(v.Generation)})
	}
	return f
}
func toScope(value integration.LabeledScope, c integration.Coordinates) (eventsequences.Scope, error) {
	scope := eventsequences.Scope{Filter: toFilter(value.Filter)}
	switch value.Expectation.Kind {
	case integration.Resolve:
		scope.Expectation = eventsequences.Resolve()
	case integration.UpperBound:
		scope.Expectation = eventsequences.Exact(events.SequenceNumber(value.Expectation.Position))
	case integration.NoMatchingEvent:
		scope.Expectation = eventsequences.NoMatchingEvent()
	case integration.NoCheck:
		scope.Expectation = eventsequences.NoCheck()
	case integration.ProviderResolved:
		resolved, ok := value.Expectation.Token.(resolvedScope)
		if !ok || resolved.coordinates != c {
			return scope, integration.ErrMismatch
		}
		return resolved.scope, nil
	default:
		return scope, integration.ErrInvalid
	}
	return scope, nil
}
