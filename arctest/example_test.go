// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arctest_test

import (
	"context"
	"fmt"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/arctest"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
)

type Greeting struct {
	Name string `json:"name" validate:"required"`
}

func (c Greeting) Handle(context.Context) (string, error) { return "Hello, " + c.Name, nil }

type Message struct {
	Text string `json:"text"`
}

func exampleMust(err error) {
	if err != nil {
		panic(err)
	}
}

func ExampleNewScenario() {
	builder, err := arc.NewBuilder(arc.Options{})
	exampleMust(err)
	exampleMust(commands.Register(builder, commands.Handle(Greeting.Handle)))
	exampleMust(queries.Register[Message](builder, "All", queries.Function(func(context.Context, queries.NoArguments) ([]Message, error) {
		return []Message{{Text: "Welcome"}}, nil
	})))
	ctx := context.Background()
	scenario, err := arctest.NewScenario(ctx, builder)
	exampleMust(err)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		exampleMust(scenario.Close(cleanup))
	}()
	command := arctest.NewCommand[Greeting, string](scenario)
	validated, err := command.Validate(ctx, Greeting{Name: "Ada"})
	exampleMust(err)
	_, responsePresent := validated.Response()
	fmt.Println("validate:", validated.IsSuccess(), responsePresent)
	result, err := command.Execute(ctx, Greeting{Name: "Ada"})
	exampleMust(err)
	response, _ := result.Response()
	fmt.Println(response)
	query := arctest.NewQuery[[]Message](scenario, "Message.All")
	snapshot, err := query.Perform(ctx, queries.Request{})
	exampleMust(err)
	messages, _ := snapshot.Data()
	fmt.Println(messages[0].Text)
	// Output:
	// validate: true false
	// Hello, Ada
	// Welcome
}
