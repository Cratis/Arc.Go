// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"context"
	"encoding"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
)

// EventSourceIDProvider overrides field-based source resolution.
type EventSourceIDProvider interface{ GetEventSourceID() EventSourceID }

// EventSourceIDValue marks a domain concept as a semantic source ID, not a raw UUID.
type EventSourceIDValue interface{ AsEventSourceID() EventSourceID }

// NewEventSourceID generates a UUID-form string once; arbitrary existing strings remain valid.
func NewEventSourceID() (EventSourceID, error) {
	id, err := concepts.NewUUID()
	return EventSourceID(id.String()), err
}

func (i *Integration) semantic(value any) (EventSourceID, bool) {
	if isNil(value) {
		return "", false
	}
	if id, ok := value.(EventSourceID); ok {
		return id, true
	}
	if id, ok := value.(*EventSourceID); ok {
		return *id, true
	}
	if id, ok := value.(EventSourceIDValue); ok {
		return id.AsEventSourceID(), true
	}
	if i.options.SemanticSource != nil {
		return i.options.SemanticSource(value)
	}
	return "", false
}
func (i *Integration) resolveSource(ctx context.Context, value any, options CommandOptions) (id EventSourceID, err error) {
	defer func() {
		if recover() != nil {
			id = ""
			err = nil
			if i.options.Logger != nil {
				i.options.Logger.DebugContext(ctx, "Source resolution failed; using Unspecified")
			}
		}
	}()
	if provider, ok := value.(EventSourceIDProvider); ok {
		return provider.GetEventSourceID(), nil
	}
	if options.Source != nil {
		return options.Source(value), nil
	}
	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", ErrInvalid
	}
	semanticType := reflect.TypeFor[EventSourceIDValue]()
	for index := 0; index < v.NumField(); index++ {
		field := v.Type().Field(index)
		if !field.IsExported() {
			continue
		}
		fv := v.Field(index)
		declared := field.Type == reflect.TypeFor[EventSourceID]() || field.Type == reflect.TypeFor[*EventSourceID]() || field.Type.Implements(semanticType) || strings.Contains(","+field.Tag.Get("arc")+",", ",key,")
		// Provider exact-type recognition may be tested with a zero value, but never
		// calls a domain concept method on that fabricated value.
		if !declared && i.options.SemanticSource != nil {
			_, declared = i.options.SemanticSource(fv.Interface())
		}
		if !declared {
			continue
		}
		if isNil(fv.Interface()) {
			return "", nil
		}
		if id, ok := i.semantic(fv.Interface()); ok {
			return id, nil
		}
		if text, ok := fv.Interface().(encoding.TextMarshaler); ok {
			body, e := text.MarshalText()
			if e != nil {
				return "", nil
			}
			return EventSourceID(body), nil
		}
		switch fv.Kind() {
		case reflect.String:
			return EventSourceID(fv.String()), nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return EventSourceID(fmt.Sprint(fv.Interface())), nil
		}
		return "", nil
	}
	return NewEventSourceID()
}

// Provide publishes an authoritative key even when source resolution is Unspecified.
func (i *Integration) Provide(ctx context.Context, inv *commands.Invocation) (commands.ContextValues, error) {
	command := inv.CommandContext()
	options := i.configurations[reflect.TypeOf(command.Command())]
	source, err := i.resolveSource(ctx, command.Command(), options)
	if err != nil {
		return commands.ContextValues{}, err
	}
	coordinates, err := i.options.StoreResolver(ctx, command)
	if err != nil {
		return commands.ContextValues{}, err
	}
	if options.Sequence != "" {
		coordinates.Sequence = options.Sequence
	}
	if coordinates.Sequence == "" {
		coordinates.Sequence = "event-log"
	}
	if !validCoordinates(coordinates) {
		return commands.ContextValues{}, ErrInvalid
	}
	if expected := MetadataFrom(ctx).Expected; expected != nil && (expected.Store != coordinates.Store || expected.Namespace != coordinates.Namespace) {
		return commands.ContextValues{}, ErrMismatch
	}
	actor := Actor{}
	if principal := command.Principal(); principal.IsAuthenticated() {
		actor = Actor{Subject: principal.ID(), Name: principal.Name()}
		if i.options.Actor != nil {
			actor = i.options.Actor(principal)
		}
	}
	causes := MetadataFrom(ctx).Causes
	parent, present, err := inv.ParentCommandContext(ctx)
	if err != nil {
		return commands.ContextValues{}, err
	}
	if present {
		if value, ok := parent.Values().Get("chronicle.causes"); ok {
			if chain, ok := value.([]Cause); ok {
				causes = cloneCauses(chain)
			}
		}
	}
	properties := map[string]string{"commandType": command.Descriptor().Type.Name, "commandTypeFullName": command.Descriptor().Type.Identity(), "eventSequenceId": string(coordinates.Sequence)}
	if i.options.Audit != nil {
		// Explicit opt-in only. No reflected nested objects or unknown sensitivity.
		budget := 8192
		values := i.options.Audit(command)
		for _, key := range slices.Sorted(maps.Keys(values)) {
			value := values[key]
			if _, reserved := properties[key]; reserved || key == "" {
				continue
			}
			runes := []rune(value)
			if len(runes) > 1024 {
				value = string(runes[:1024]) + "…"
			}
			if len(key)+len(value) > budget {
				continue
			}
			budget -= len(key) + len(value)
			properties[key] = value
		}
	}
	causes = append(causes, Cause{Occurred: command.ReceivedAt(), Type: "Command", Properties: properties})
	frame := &commandFrame{coordinates: coordinates, source: source, options: options, actor: actor, causes: causes, scopes: map[string]LabeledScope{}}
	if err := commands.SetFrameState(ctx, inv, currentIntegration, i); err != nil {
		return commands.ContextValues{}, err
	}
	if err := commands.SetFrameState(ctx, inv, i.frame, frame); err != nil {
		return commands.ContextValues{}, err
	}
	return commands.NewContextValues(map[string]any{commands.ResolvedKey: string(source), "chronicle.causes": cloneCauses(causes)})
}
