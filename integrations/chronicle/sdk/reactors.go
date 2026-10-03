// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"maps"
	"reflect"

	"github.com/cratis/arc.go/correlation"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/reactors"
)

// DeliveryFrom copies per-event metadata. Inconsistent observer/event coordinates
// produce an invalid delivery that ReactorCommands refuses before command execution.
// CausedBy is deliberately not translated to a principal or roles.
func DeliveryFrom(event events.Context, delivery reactors.Delivery) integration.Delivery {
	if event.Store != delivery.Store || event.Namespace != delivery.Namespace || event.Sequence != delivery.Sequence || event.SourceID != delivery.Partition || event.SequenceNumber != delivery.SequenceNumber {
		return integration.Delivery{}
	}
	value := integration.Delivery{ID: delivery.ID(), Coordinates: integration.Coordinates{Store: integration.StoreName(delivery.Store), Namespace: integration.Namespace(delivery.Namespace), Sequence: integration.SequenceID(delivery.Sequence)}, Correlation: correlation.ID([16]byte(event.CorrelationID)), Replay: event.ObservationState&events.ObservationReplay != 0}
	for _, cause := range event.Causation {
		value.Causes = append(value.Causes, integration.Cause{Occurred: cause.Occurred, Type: cause.Type, Properties: maps.Clone(cause.Properties)})
	}
	return value
}

type commandEffects struct {
	bridge *integration.ReactorCommands
	types  map[reflect.Type]bool
}

// CommandEffects compiles exact command return claims before NewClient. Register
// this handler with chronicle.RegisterReactorSideEffectHandler. Collections of
// claimed commands are preflighted and executed in order by the SDK; failures
// occur before acknowledgement. Bind bridge to the built Arc app before delivery.
func CommandEffects(bridge *integration.ReactorCommands, types ...reflect.Type) (reactors.SideEffectHandler, error) {
	if bridge == nil || len(types) == 0 {
		return nil, integration.ErrInvalid
	}
	result := &commandEffects{bridge: bridge, types: map[reflect.Type]bool{}}
	for _, typ := range types {
		if typ == nil || typ.Kind() == reflect.Interface || result.types[typ] {
			return nil, integration.ErrInvalid
		}
		result.types[typ] = true
	}
	return result, nil
}
func (h *commandEffects) CanHandleReturnType(typ reflect.Type) bool { return h.types[typ] }
func (h *commandEffects) CanHandle(_ reactors.SideEffectContext, value any) bool {
	return h.types[reflect.TypeOf(value)]
}
func (h *commandEffects) Handle(ctx context.Context, inv reactors.SideEffectContext, value any) error {
	return h.bridge.Execute(ctx, DeliveryFrom(inv.Context, inv.Delivery), value)
}
