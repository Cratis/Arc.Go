// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package fixture builds the small Arc application every recipe mounts, and
// verifies the Arc HTTP behavior a host must preserve through a real server.
package fixture

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

// Paths owned by the fixture application.
const (
	CommandPath   = "/api/tasks/register-task"
	ValidatePath  = CommandPath + "/validate"
	QueryPath     = "/api/tasks/search"
	SlowQueryPath = "/api/tasks/slow"
	LiveQueryPath = "/api/tasks/live"
	UnmappedPath  = "/api/tasks/unmapped"
)

// RegisterTask is the fixture command; Arc's portable tag requires a title.
type RegisterTask struct {
	Title string `json:"title" validate:"required"`
}

// Task is the fixture read model.
type Task struct {
	Title string `json:"title"`
}

// SearchArguments are bound from the GET query string or the QUERY body.
type SearchArguments struct {
	Title string `json:"title"`
}

// Application is a started Arc application plus observation points for tests.
type Application struct {
	// App is the started Arc application, an http.Handler.
	App *arc.Application
	// Handled counts executed RegisterTask handlers.
	Handled atomic.Int32
	// SlowStarted is closed once the slow query is executing.
	SlowStarted chan struct{}
	// SlowStopped receives the context error the slow query observed.
	SlowStopped chan error
	// LiveStopped receives each observable producer's cancellation on cleanup.
	LiveStopped chan error

	slowOnce atomic.Bool
}

// Configure customizes the builder before the fixture registers its artifacts.
type Configure func(*arc.Builder) error

// New builds and starts the fixture application and shuts it down on cleanup.
func New(t testing.TB, options arc.Options, configure ...Configure) *Application {
	t.Helper()
	f := &Application{SlowStarted: make(chan struct{}), SlowStopped: make(chan error, 1), LiveStopped: make(chan error, 4)}
	builder, err := arc.NewBuilder(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range configure {
		if err := c(builder); err != nil {
			t.Fatal(err)
		}
	}
	if err := commands.Register[RegisterTask](builder, commands.Handle(func(RegisterTask, context.Context) (string, error) {
		f.Handled.Add(1)
		return "registered", nil
	}), commands.WithPath[RegisterTask](CommandPath)); err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[Task](builder, "Search", queries.Function(func(_ context.Context, a SearchArguments) ([]Task, error) {
		return []Task{{Title: a.Title}}, nil
	}), queries.WithPath[SearchArguments](QueryPath)); err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[Task](builder, "Slow", queries.Function(func(ctx context.Context, _ queries.NoArguments) ([]Task, error) {
		if f.slowOnce.CompareAndSwap(false, true) {
			close(f.SlowStarted)
		}
		<-ctx.Done()
		f.SlowStopped <- ctx.Err()
		return nil, ctx.Err()
	}), queries.WithPath[queries.NoArguments](SlowQueryPath)); err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterObservable[Task](builder, "Live", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[Task], error) {
		return observable.FromProducer(func(ctx context.Context, emit func(Task) error) error {
			defer func() { f.LiveStopped <- ctx.Err() }()
			for _, title := range []string{"first", "second"} {
				if err := emit(Task{Title: title}); err != nil {
					return err
				}
			}
			<-ctx.Done()
			return ctx.Err()
		}), nil
	}), queries.WithPath[queries.NoArguments](LiveQueryPath)); err != nil {
		t.Fatal(err)
	}
	app, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	f.App = app
	return f
}
