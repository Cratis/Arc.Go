// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Command hosting runs a minimal Arc HTTP application without a container.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
)

// Greet is an ordinary model-bound command with required input.
type Greet struct {
	Name string `json:"name" validate:"required"`
}

// Handle returns a client response without a database or event store.
func (c Greet) Handle(_ context.Context) (string, error) { return "Hello, " + c.Name, nil }

func run(ctx context.Context) error {
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		return err
	}
	if err := commands.Register[Greet](builder, commands.Handle(Greet.Handle)); err != nil {
		return err
	}
	app, err := builder.Build()
	if err != nil {
		return err
	}
	return app.Run(ctx, "127.0.0.1:8080")
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
