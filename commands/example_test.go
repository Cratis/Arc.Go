// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"fmt"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type Greet struct {
	Name string `json:"name"`
}

func (c Greet) Handle(context.Context) (string, error) { return "Hello, " + c.Name, nil }
func ExampleHandle() {
	var registry commands.Registry
	if err := commands.Register(&registry, commands.Handle(Greet.Handle)); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := commands.Execute[string](context.Background(), pipeline, Greet{Name: "Ada"})
	if err != nil {
		panic(err)
	}
	response, present := result.Response()
	fmt.Println(result.IsSuccess(), present, response)
	// Output: true true Hello, Ada
}

type ClearLocalCache struct{}

func (ClearLocalCache) Handle(context.Context) error { return nil }
func ExampleRegister() {
	var registry commands.Registry
	if err := commands.Register[ClearLocalCache](&registry); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := pipeline.Execute(context.Background(), ClearLocalCache{})
	if err != nil {
		panic(err)
	}
	_, present := result.Response()
	fmt.Println(result.IsSuccess(), present)
	// Output: true false
}

type Multiplier interface{ Multiply(int) int }
type multiplier int

func (m multiplier) Multiply(value int) int { return int(m) * value }

type Calculate struct {
	Number int `json:"number"`
}

func (c Calculate) Handle(_ context.Context, service Multiplier) (int, error) {
	return service.Multiply(c.Number), nil
}
func ExampleHandle_dependencies() {
	service := multiplier(2)
	var registry commands.Registry
	if err := commands.Register(&registry, commands.Handle(func(c Calculate, ctx context.Context) (int, error) { return c.Handle(ctx, service) })); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := commands.Execute[int](context.Background(), pipeline, Calculate{Number: 21})
	if err != nil {
		panic(err)
	}
	response, _ := result.Response()
	fmt.Println(response)
	// Output: 42
}

type AddItem struct {
	Name     string `json:"name" validate:"required"`
	Quantity int    `json:"quantity"`
}

func (c AddItem) Validate(context.Context) ([]validation.Result, error) {
	if c.Quantity > 0 {
		return nil, nil
	}
	return []validation.Result{{Severity: validation.Error, Message: "Quantity must be greater than zero.", Members: []string{"quantity"}}}, nil
}
func (AddItem) Handle(context.Context) error { return nil }
func ExamplePipeline_Validate() {
	var registry commands.Registry
	if err := commands.Register[AddItem](&registry); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := pipeline.Validate(context.Background(), AddItem{Name: "Book", Quantity: 0})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.IsAuthorized(), result.IsValid(), result.Details().ValidationResults[0].Members[0])
	// Output: true false quantity
}

type Double struct {
	Number int `json:"number"`
}

func (c Double) Provide(context.Context) (int, error)           { return c.Number * 2, nil }
func (Double) Handle(_ context.Context, value int) (int, error) { return value, nil }
func ExampleWithProvide() {
	var registry commands.Registry
	if err := commands.Register(&registry, commands.WithProvide(Double.Provide, Double.Handle)); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := commands.Execute[int](context.Background(), pipeline, Double{Number: 21})
	if err != nil {
		panic(err)
	}
	response, _ := result.Response()
	fmt.Println(response)
	// Output: 42
}

type CalculationResources struct {
	Service Multiplier
	Closed  bool
}

func (r *CalculationResources) Close(context.Context) error { r.Closed = true; return nil }
func ExampleScoped() {
	resources := &CalculationResources{Service: multiplier(2)}
	var registry commands.Registry
	if err := commands.Register(&registry, commands.Scoped(func(_ context.Context, r *CalculationResources) (commands.Handler[Calculate, int], error) {
		return commands.Handle(func(c Calculate, ctx context.Context) (int, error) { return c.Handle(ctx, r.Service) }), nil
	})); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
	if err != nil {
		panic(err)
	}
	result, err := commands.Execute[int](context.Background(), pipeline, Calculate{Number: 21})
	if err != nil {
		panic(err)
	}
	response, _ := result.Response()
	fmt.Println(response, resources.Closed)
	// Output: 42 true
}

type ItemAdded struct {
	Name string `json:"name"`
}
type AddNamedItem struct {
	Name string `json:"name"`
}

func (c AddNamedItem) Handle(context.Context) (commands.Outcome[string], error) {
	return commands.Respond("receipt", ItemAdded(c)), nil
}

type eventRecorder struct{ Events []ItemAdded }

func (*eventRecorder) CanHandle(_ commands.CommandContext, value any) bool {
	_, ok := value.(ItemAdded)
	return ok
}
func (r *eventRecorder) Handle(_ context.Context, inv *commands.Invocation, value any) (commands.Result[commands.NoResponse], error) {
	r.Events = append(r.Events, value.(ItemAdded))
	return commands.Success(inv.CommandContext().CorrelationID()), nil
}
func ExampleRespond() {
	// This is an in-memory extension, not Chronicle append or a transaction.
	recorder := &eventRecorder{}
	var registry commands.Registry
	if err := commands.Register(&registry, commands.Handle(AddNamedItem.Handle)); err != nil {
		panic(err)
	}
	if err := commands.RegisterResponseValueHandler[ItemAdded](&registry, "events", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) { return recorder, nil }); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	result, err := commands.Execute[string](context.Background(), pipeline, AddNamedItem{Name: "Book"})
	if err != nil {
		panic(err)
	}
	response, _ := result.Response()
	fmt.Println(response, recorder.Events[0].Name)
	// Output: receipt Book
}
