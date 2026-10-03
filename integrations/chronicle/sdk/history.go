// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"fmt"
	"reflect"

	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
)

func (a *adapter) ReadHistory(ctx context.Context, r integration.HistoryRequest) (integration.History, error) {
	sequence, err := a.sequence(ctx, r.Coordinates)
	if err != nil {
		return integration.History{}, err
	}
	filter := toFilter(r.Filter)
	history, err := sequence.ReadHistory(auditContext(ctx), events.SourceID(r.Filter.Source), eventsequences.SourceFilter{SourceType: events.SourceType(r.Filter.Route.SourceType), StreamType: events.StreamType(r.Filter.Route.StreamType), StreamID: events.StreamID(r.Filter.Route.StreamID), EventTypes: filter.EventTypes})
	if err != nil {
		return integration.History{}, err
	}
	return a.decodeHistory(r, history)
}

func (a *adapter) decodeHistory(r integration.HistoryRequest, history eventsequences.History) (integration.History, error) {
	result := integration.History{Scope: integration.LabeledScope{Label: string(r.Filter.Source), Filter: fromFilter(history.Filter), Expectation: integration.Expectation{Kind: integration.NoMatchingEvent}}}
	for _, recorded := range history.Events {
		descriptor, ok := a.events.LookupRef(recorded.Context.EventType)
		if !ok {
			return integration.History{}, fmt.Errorf("%w: historical event generation", integration.ErrUnsupported)
		}
		value := reflect.New(descriptor.GoType())
		if err := descriptor.Unmarshal(recorded.Content, value.Interface()); err != nil {
			return integration.History{}, err
		}
		result.Events = append(result.Events, integration.RecordedEvent{Event: value.Elem().Interface(), Type: integration.EventType{ID: string(recorded.Context.EventType.ID), Generation: uint32(recorded.Context.EventType.Generation)}, Position: uint64(recorded.Context.SequenceNumber)})
	}
	if len(result.Events) > 0 {
		result.Scope.Expectation = integration.Expectation{Kind: integration.UpperBound, Position: result.Events[len(result.Events)-1].Position}
	}
	return result, nil
}
