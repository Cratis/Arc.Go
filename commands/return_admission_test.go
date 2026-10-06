// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
)

func TestTypedNilReturnAdmissionIsOptInAndExplicitResponseIsPreserved(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, explicit := range []bool{true, false} {
			var r commands.Registry
			must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (any, error) {
				var event *Event
				if explicit {
					return commands.Respond(event), nil
				}
				return event, nil
			})))
			if enabled {
				must(t, r.AddReturnAdmission("events", commands.ReturnAdmission{Types: []reflect.Type{reflect.TypeFor[*Event]()}}))
			}
			result, err := build(t, &r, commands.PipelineOptions{}).Execute(t.Context(), Clear{})
			if enabled && !explicit {
				if !errors.Is(err, commands.ErrNilReturn) || result.IsSuccess() {
					t.Fatal(result, err)
				}
			} else {
				must(t, err)
				if !result.IsSuccess() {
					t.Fatal(result)
				}
			}
		}
	}
}

func TestStaticAdmissionCompilesMetadataAndKeepsAdditiveConsumers(t *testing.T) {
	var r commands.Registry
	checks, consumers := 0, 0
	types := []reflect.Type{reflect.TypeFor[Event]()}
	must(t, r.AddReturnAdmission("events", commands.ReturnAdmission{Types: types, Check: func(context.Context, *commands.Invocation, any) (commands.ReturnClassification, error) {
		checks++
		return commands.OrdinaryReturn, nil
	}}))
	types[0] = reflect.TypeFor[int]() // Registration owns its catalog membership.
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (Event, error) { return Event{}, nil })))
	for _, name := range []string{"one", "two"} {
		must(t, commands.RegisterResponseValueHandler[Event](&r, name, func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
			return consumer{can: func(commands.CommandContext, any) bool { return true }, handle: func(_ context.Context, inv *commands.Invocation, _ any) (commands.Result[commands.NoResponse], error) {
				consumers++
				return commands.Success(inv.CommandContext().CorrelationID()), nil
			}}, nil
		}))
	}
	p := build(t, &r, commands.PipelineOptions{})
	registration, err := p.LookupCommand(Clear{})
	must(t, err)
	if registration.ResponseKind() != commands.ResponseNone || checks != 0 {
		t.Fatal(registration.ResponseKind(), checks)
	}
	result, err := commands.Execute[commands.NoResponse](t.Context(), p, Clear{})
	must(t, err)
	if _, response := result.Response(); response || checks != 1 || consumers != 2 {
		t.Fatal(response, checks, consumers)
	}
}

func TestReturnAdmissionPreflightsAllLeavesBeforeAnyConsumer(t *testing.T) {
	var r commands.Registry
	cause := errors.New("invalid metadata")
	must(t, r.AddReturnAdmission("events", commands.ReturnAdmission{Types: []reflect.Type{reflect.TypeFor[Event]()}, Check: func(_ context.Context, _ *commands.Invocation, value any) (commands.ReturnClassification, error) {
		if value.(Event).Name == "invalid" {
			return commands.ServerConsumedReturn, cause
		}
		return commands.ServerConsumedReturn, nil
	}}))
	must(t, r.AddResponseValueHandler("consumer", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
		return updatingConsumer{consumer: consumer{can: func(commands.CommandContext, any) bool { t.Fatal("predicate ran before preflight"); return true }, handle: func(context.Context, *commands.Invocation, any) (commands.Result[commands.NoResponse], error) {
			t.Fatal("consumer ran")
			return commands.Result[commands.NoResponse]{}, nil
		}}, update: func(context.Context, *commands.Invocation, any) error { t.Fatal("updater ran"); return nil }}, nil
	}))
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (commands.Outcome[int], error) {
		return commands.Respond(42, Event{Name: "valid"}, Event{Name: "invalid"}), nil
	})))
	result, err := build(t, &r, commands.PipelineOptions{}).Execute(t.Context(), Clear{})
	if result.IsSuccess() || !errors.Is(err, cause) {
		t.Fatal(result, err)
	}
}

func TestDynamicReturnAdmissionRetainsUnknownMetadataAndRequiresConsumer(t *testing.T) {
	var r commands.Registry
	must(t, r.AddReturnAdmission("dynamic", commands.ReturnAdmission{Check: func(_ context.Context, _ *commands.Invocation, value any) (commands.ReturnClassification, error) {
		if _, event := value.(Event); event {
			return commands.ServerConsumedReturn, nil
		}
		return commands.OrdinaryReturn, nil
	}}))
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (Event, error) { return Event{}, nil })))
	p := build(t, &r, commands.PipelineOptions{})
	registration, err := p.LookupCommand(Clear{})
	must(t, err)
	if registration.ResponseKind() != commands.ResponseUnknown {
		t.Fatal(registration.ResponseKind())
	}
	result, err := p.Execute(t.Context(), Clear{})
	if !errors.Is(err, commands.ErrUnhandledEffect) || result.IsSuccess() {
		t.Fatal(result, err)
	}
}

func TestExplicitEventResponseBypassesAdmissionAndConsumption(t *testing.T) {
	var r commands.Registry
	must(t, r.AddReturnAdmission("events", commands.ReturnAdmission{Types: []reflect.Type{reflect.TypeFor[Event]()}, Check: func(context.Context, *commands.Invocation, any) (commands.ReturnClassification, error) {
		t.Fatal("explicit response admitted as effect")
		return commands.OrdinaryReturn, nil
	}}))
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (commands.Outcome[Event], error) {
		return commands.Respond(Event{Name: "response"}), nil
	})))
	result, err := commands.Execute[Event](t.Context(), build(t, &r, commands.PipelineOptions{}), Clear{})
	must(t, err)
	if value, present := result.Response(); !present || value.Name != "response" {
		t.Fatal(value, present)
	}
}
