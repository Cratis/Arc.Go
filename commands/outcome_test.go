// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type Event struct{ Name string }
type consumer struct {
	can    func(commands.CommandContext, any) bool
	handle func(context.Context, *commands.Invocation, any) (commands.Result[commands.NoResponse], error)
}

func (h consumer) CanHandle(c commands.CommandContext, v any) bool { return h.can(c, v) }
func (h consumer) Handle(ctx context.Context, inv *commands.Invocation, v any) (commands.Result[commands.NoResponse], error) {
	return h.handle(ctx, inv, v)
}

type updatingConsumer struct {
	consumer
	update func(context.Context, *commands.Invocation, any) error
}

func (h updatingConsumer) UpdateContext(ctx context.Context, inv *commands.Invocation, v any) error {
	return h.update(ctx, inv, v)
}

type eventMarker interface{ EventName() string }
type markedEvent struct{}

func (markedEvent) EventName() string { return "created" }

func TestTypedInterfaceConsumersMatchImplementationsButConcreteTypesStayExact(t *testing.T) {
	for _, exact := range []bool{false, true} {
		var r commands.Registry
		must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (*markedEvent, error) { return &markedEvent{}, nil })))
		calls := 0
		factory := func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
			return consumer{can: func(commands.CommandContext, any) bool { return true }, handle: func(_ context.Context, inv *commands.Invocation, _ any) (commands.Result[commands.NoResponse], error) {
				calls++
				return commands.Success(inv.CommandContext().CorrelationID()), nil
			}}, nil
		}
		if exact {
			must(t, commands.RegisterResponseValueHandler[markedEvent](&r, "events", factory))
		} else {
			must(t, commands.RegisterResponseValueHandler[eventMarker](&r, "events", factory))
		}
		p := build(t, &r, commands.PipelineOptions{})
		result, err := p.Execute(t.Context(), Clear{})
		must(t, err)
		_, present := result.Response()
		if !result.IsSuccess() || present != exact || calls != map[bool]int{true: 0, false: 1}[exact] {
			t.Fatal(result.Details(), present, calls)
		}
	}
}

func TestAdditiveHandlersAndResponseDependentPredicates(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (commands.Outcome[string], error) {
		return commands.Values[string]("new-id", Event{"created"}), nil
	})))
	order := []string{}
	for _, name := range []string{"first", "second"} {
		must(t, commands.RegisterResponseValueHandler[Event](&r, name, func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
			return consumer{can: func(c commands.CommandContext, v any) bool {
				response, present := c.Response()
				return present && response == "new-id"
			}, handle: func(_ context.Context, inv *commands.Invocation, _ any) (commands.Result[commands.NoResponse], error) {
				order = append(order, name)
				return commands.Success(inv.CommandContext().CorrelationID()), nil
			}}, nil
		}))
	}
	p := build(t, &r, commands.PipelineOptions{})
	result, err := commands.Execute[string](t.Context(), p, Clear{})
	must(t, err)
	if response, present := result.Response(); !result.IsSuccess() || !present || response != "new-id" || !reflect.DeepEqual(order, []string{"first", "second"}) {
		t.Fatal(result.Details(), order)
	}
}
func TestUpdaterPassBeforeResponseSelection(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (commands.Outcome[int], error) {
		return commands.Values[int](Event{"effect"}, 0), nil
	})))
	updates, consumed := 0, 0
	must(t, r.AddResponseValueHandler("updater", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
		return updatingConsumer{consumer: consumer{can: func(c commands.CommandContext, value any) bool { _, event := value.(Event); return event }, handle: func(_ context.Context, inv *commands.Invocation, _ any) (commands.Result[commands.NoResponse], error) {
			consumed++
			if value, ok := inv.CommandContext().Values().Get("updated"); !ok || value != true {
				t.Fatal("updater values missing")
			}
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}}, update: func(_ context.Context, inv *commands.Invocation, _ any) error {
			updates++
			return inv.SetValue("updated", true)
		}}, nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := commands.Execute[int](t.Context(), p, Clear{})
	must(t, err)
	if value, present := result.Response(); !present || value != 0 || updates != 1 || consumed != 1 {
		t.Fatal(value, present, updates, consumed)
	}
}
func TestAmbiguityAndControlsPreflightBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome commands.Outcome[any]
		want    error
		valid   bool
	}{
		{"multiple", commands.Values[any](1, 2, Event{}), commands.ErrMultipleResponses, false},
		{"unconsumed effect", commands.Effects[any](1), commands.ErrUnhandledEffect, false},
		{"returned warning", commands.Values[any](Event{}, validation.Result{Severity: validation.Warning}), nil, false},
		{"explicit control", commands.Respond[any](1, Event{}, commands.Control[any](commands.WithValidationResults(correlation.ID{}, validation.Result{Severity: validation.Information}))), nil, false},
		{"denial", commands.Values[any](Event{}, authorization.Deny("not allowed")), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r commands.Registry
			must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (commands.Outcome[any], error) { return tc.outcome, nil })))
			calls := 0
			must(t, commands.RegisterResponseValueHandler[Event](&r, "events", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
				return consumer{can: func(commands.CommandContext, any) bool { return true }, handle: func(_ context.Context, inv *commands.Invocation, _ any) (commands.Result[commands.NoResponse], error) {
					calls++
					return commands.Success(inv.CommandContext().CorrelationID()), nil
				}}, nil
			}))
			p := build(t, &r, commands.PipelineOptions{})
			result, err := p.Execute(t.Context(), Clear{})
			if !errors.Is(err, tc.want) || result.IsSuccess() != tc.valid || calls != 0 {
				t.Fatal(result.Details(), err, calls)
			}
			if _, present := result.Response(); present {
				t.Fatal("failure published response")
			}
		})
	}
}
func TestTypedNilScalarZerosAndCollectionLeaves(t *testing.T) {
	for _, value := range []any{0, false, "", []int{1, 2}, map[string]int{"one": 1}, [2]int{1, 2}, commands.Success(correlation.ID{}), []validation.Result{{Severity: validation.Error}}} {
		var r commands.Registry
		must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (any, error) { return value, nil })))
		p := build(t, &r, commands.PipelineOptions{})
		result, err := p.Execute(t.Context(), Clear{})
		must(t, err)
		if got, present := result.Response(); !result.IsSuccess() || !present || !reflect.DeepEqual(got, value) {
			t.Fatalf("value %T lost: %+v", value, result.Details())
		}
	}
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (commands.Outcome[int], error) {
		var absent *Event
		return commands.Values[int](absent, 0), nil
	})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	if response, present := result.Response(); !present || response != 0 {
		t.Fatal(response, present)
	}
}

type OrderID string

func TestTypedUnrelatedEventHandlerPreservesKnownOrderIDResponse(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (OrderID, error) { return OrderID("order-1"), nil })))
	must(t, commands.RegisterResponseValueHandler[Event](&r, "events", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
		return consumer{can: func(commands.CommandContext, any) bool { return true }, handle: func(context.Context, *commands.Invocation, any) (commands.Result[commands.NoResponse], error) {
			t.Fatal("unrelated handler consumed OrderID")
			return commands.Result[commands.NoResponse]{}, nil
		}}, nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	registration, _ := p.Lookup("Clear")
	if typ, present := registration.ResponseType(); registration.ResponseKind() != commands.ResponseValue || !present || typ != reflect.TypeFor[OrderID]() {
		t.Fatal("unrelated typed handler erased response metadata")
	}
	result, err := commands.Execute[OrderID](t.Context(), p, Clear{})
	must(t, err)
	if value, present := result.Response(); !result.IsSuccess() || !present || value != "order-1" {
		t.Fatal(result.Details(), value)
	}
}

func TestUnknownAndEnforcedResponseContracts(t *testing.T) {
	var r commands.Registry
	calls := 0
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (int, error) { calls++; return 1, nil })))
	must(t, r.AddResponseValueHandler("conditional", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
		return consumer{can: func(commands.CommandContext, any) bool { return false }, handle: func(context.Context, *commands.Invocation, any) (commands.Result[commands.NoResponse], error) {
			return commands.Result[commands.NoResponse]{}, nil
		}}, nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	registration, _ := p.Lookup("Clear")
	if registration.ResponseKind() != commands.ResponseUnknown {
		t.Fatal("dynamic handler guessed response")
	}
	typed, err := commands.Execute[int](t.Context(), p, Clear{})
	must(t, err)
	if value, present := typed.Response(); !typed.IsSuccess() || !present || value != 1 || calls != 1 {
		t.Fatal("unknown contract was refused before runtime assertion", typed.Details(), value, calls)
	}
	mismatch, err := commands.Execute[string](t.Context(), p, Clear{})
	if !errors.Is(err, commands.ErrResponseType) || mismatch.IsSuccess() || calls != 2 {
		t.Fatal("runtime mismatch reported success", mismatch.Details(), err, calls)
	}
	if _, present := mismatch.Response(); present {
		t.Fatal("runtime mismatch retained response")
	}
	var noResponse commands.Registry
	must(t, commands.Register[Clear](&noResponse, commands.Handle(func(Clear, context.Context) (int, error) { return 1, nil }), commands.WithNoResponse[Clear]()))
	p = build(t, &noResponse, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Clear{})
	if !errors.Is(err, commands.ErrResponseType) || result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
}
func TestPostHandleWarningIsNeverInputFiltered(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (validation.Result, error) {
		return validation.Result{Severity: validation.Warning, Message: "warning"}, nil
	})))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Clear{}, commands.ExecuteOptions{AllowedSeverity: severity(validation.Error)})
	must(t, err)
	if result.IsValid() || len(result.Details().ValidationResults) != 1 {
		t.Fatal(result.Details())
	}
}
