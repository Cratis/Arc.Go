// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/cratis/arc.go/commands"
)

// Subject overrides compliance identity, never authentication.
type Subject string

// EventValue is an explicitly targeted event; zero is invalid.
type EventValue struct {
	entry    Entry
	explicit bool
	err      error
}

// EventBatch is an explicit ordered event collection. Zero means no event work.
type EventBatch struct {
	values []EventValue
	scopes []LabeledScope
}

// EventOption configures copied metadata at wrapper construction.
type EventOption func(*Entry)

// Events declares bare events at the command's eventual response-selected source.
func Events(values ...any) EventBatch {
	batch := EventBatch{}
	for _, value := range values {
		if wrapped, ok := value.(EventValue); ok {
			batch.values = append(batch.values, wrapped)
		} else {
			batch.values = append(batch.values, EventValue{entry: Entry{Event: value}})
		}
	}
	return batch
}

// EventForSource retains its explicit destination even when a response retargets bare events.
func EventForSource(id EventSourceID, event any, options ...EventOption) EventValue {
	result := EventValue{entry: Entry{Source: id, Event: event}, explicit: true}
	for _, option := range options {
		if option == nil {
			result.err = ErrInvalid
			continue
		}
		option(&result.entry)
	}
	result.entry = cloneEntry(result.entry)
	return result
}

// EventsWithScopes copies membership and scope metadata. Tokens must be immutable.
func EventsWithScopes(entries []EventValue, scopes ...LabeledScope) EventBatch {
	return EventBatch{values: slices.Clone(entries), scopes: cloneScopes(scopes)}
}
func WithRoute(route Route) EventOption      { return func(e *Entry) { e.Route = route } }
func WithSubject(subject string) EventOption { return func(e *Entry) { e.Subject = &subject } }
func WithOccurred(at time.Time) EventOption  { return func(e *Entry) { e.Occurred = &at } }
func WithTags(tags ...string) EventOption {
	tags = slices.Clone(tags)
	return func(e *Entry) { e.Tags = slices.Clone(tags) }
}
func WithNamedTags(tags ...NamedTag) EventOption {
	tags = slices.Clone(tags)
	return func(e *Entry) { e.NamedTags = slices.Clone(tags) }
}
func WithCausation(causes ...Cause) EventOption {
	causes = cloneCauses(causes)
	return func(e *Entry) { e.Causation = cloneCauses(causes) }
}
func cloneCauses(values []Cause) []Cause {
	result := slices.Clone(values)
	for index := range result {
		result[index].Properties = maps.Clone(result[index].Properties)
	}
	return result
}
func cloneScopes(values []LabeledScope) []LabeledScope {
	result := slices.Clone(values)
	for index := range result {
		result[index].Filter.Events = slices.Clone(result[index].Filter.Events)
	}
	return result
}
func cloneEntry(e Entry) Entry {
	e.Tags = slices.Clone(e.Tags)
	e.NamedTags = slices.Clone(e.NamedTags)
	e.Causation = cloneCauses(e.Causation)
	if e.Subject != nil {
		value := *e.Subject
		e.Subject = &value
	}
	if e.Occurred != nil {
		value := *e.Occurred
		e.Occurred = &value
	}
	return e
}
func (i *Integration) descriptor(value any) (EventDescriptor, bool) {
	typ := reflect.TypeOf(value)
	if typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	d, ok := i.events[typ]
	return d, ok
}
func (i *Integration) preflight(value EventValue) error {
	if value.err != nil {
		return value.err
	}
	e := value.entry
	if isNil(e.Event) {
		return commands.ErrNilReturn
	}
	d, ok := i.descriptor(e.Event)
	if !ok {
		return ErrNotRegistered
	}
	if value.explicit && strings.TrimSpace(string(e.Source)) == "" {
		return ErrInvalid
	}
	if e.Subject != nil && strings.TrimSpace(*e.Subject) == "" {
		return ErrInvalid
	}
	if e.Occurred != nil && (e.Occurred.Year() < 1 || e.Occurred.Year() > 9999) {
		return ErrInvalid
	}
	for _, tag := range e.NamedTags {
		if strings.TrimSpace(tag.Name) == "" {
			return ErrInvalid
		}
	}
	return d.Validate(e.Event)
}
func (i *Integration) admit(_ context.Context, _ *commands.Invocation, value any) (commands.ReturnClassification, error) {
	switch value := value.(type) {
	case EventValue:
		return commands.ServerConsumedReturn, i.preflight(value)
	case EventBatch:
		for _, event := range value.values {
			if err := i.preflight(event); err != nil {
				return commands.ServerConsumedReturn, err
			}
		}
		for _, scope := range value.scopes {
			if err := validateScope(scope); err != nil {
				return commands.ServerConsumedReturn, err
			}
		}
		return commands.ServerConsumedReturn, nil
	default:
		return commands.ServerConsumedReturn, i.preflight(EventValue{entry: Entry{Event: value}})
	}
}
func validateScope(scope LabeledScope) error {
	if strings.TrimSpace(scope.Label) == "" || (scope.Filter.Source != "" && string(scope.Filter.Source) != scope.Label) || scope.Expectation.Kind > ProviderResolved {
		return ErrInvalid
	}
	if scope.Expectation.Kind == UpperBound && scope.Expectation.Position >= ^uint64(0)-2 {
		return ErrInvalid
	}
	if scope.Expectation.Kind == ProviderResolved && scope.Expectation.Token == nil {
		return ErrInvalid
	}
	return nil
}
func (i *Integration) CanHandle(_ commands.CommandContext, value any) bool {
	switch value.(type) {
	case EventValue, EventBatch, Subject:
		return true
	}
	_, ok := i.descriptor(value)
	return ok
}
func (i *Integration) UpdateContext(_ context.Context, inv *commands.Invocation, value any) error {
	if subject, ok := value.(Subject); ok {
		if strings.TrimSpace(string(subject)) == "" {
			return ErrInvalid
		}
		return inv.SetValue("chronicle.subject", string(subject))
	}
	return nil
}
func (i *Integration) Handle(ctx context.Context, inv *commands.Invocation, value any) (commands.Result[commands.NoResponse], error) {
	success := commands.Success(inv.CommandContext().CorrelationID())
	if _, ok := value.(Subject); ok {
		return success, nil
	}
	frame, err := i.frameFor(ctx, inv)
	if err != nil {
		return success, err
	}
	target := frame.source
	if response, present := inv.CommandContext().Response(); present {
		if id, semantic := i.semantic(response); semantic {
			target = id
		}
	}
	batch := EventBatch{}
	switch value := value.(type) {
	case EventValue:
		batch.values = []EventValue{value}
	case EventBatch:
		batch = value
	default:
		batch = Events(value)
	}
	enrollment := Batch{Scopes: cloneScopes(batch.scopes)}
	for _, value := range batch.values {
		entry := cloneEntry(value.entry)
		if !value.explicit {
			entry.Source = target
		}
		if entry.Route == (Route{}) {
			entry.Route = frame.options.Route
		}
		if entry.Subject == nil && frame.options.Subject != nil {
			entry.Subject = frame.options.Subject(inv.CommandContext().Command())
		}
		if subject, present := inv.CommandContext().Values().Get("chronicle.subject"); present && entry.Subject == nil {
			v := subject.(string)
			entry.Subject = &v
		}
		enrollment.Entries = append(enrollment.Entries, entry)
	}
	if err := i.stage(ctx, inv, enrollment); err != nil {
		return success, err
	}
	return success, nil
}
