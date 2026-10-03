// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package taskboard is the Go-owned real-pipeline HTTP conformance fixture.
package taskboard

import (
	"context"
	"fmt"
	"sync"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/queries"
)

// Task is the complete task-board snapshot.
type Task struct {
	ID        concepts.UUID `json:"id"`
	Title     string        `json:"title"`
	Completed bool          `json:"completed"`
}

// Created is the create-command client response.
type Created struct {
	ID    concepts.UUID `json:"id"`
	Title string        `json:"title"`
}

// CreateTask is model-bound create input.
type CreateTask struct {
	Title string `json:"title" validate:"required"`
}

// CompleteTask is model-bound completion input.
type CompleteTask struct {
	TaskID concepts.UUID `json:"taskId"`
}

// ByID supplies GET and QUERY identifier arguments.
type ByID struct {
	ID concepts.UUID `json:"id"`
}

type board struct {
	mu    sync.Mutex
	tasks []Task
}

// New builds an independent fixture with standard-library state and real Arc
// registrations. Start or Serve owns admission; no handwritten envelopes exist.
func New() (*arc.Application, error) {
	state := &board{tasks: []Task{}}
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		return nil, err
	}
	if err := commands.Register[CreateTask](b, commands.Handle(func(c CreateTask, ctx context.Context) (Created, error) {
		if err := ctx.Err(); err != nil {
			return Created{}, err
		}
		id, err := concepts.NewUUID()
		if err != nil {
			return Created{}, err
		}
		state.mu.Lock()
		state.tasks = append(state.tasks, Task{ID: id, Title: c.Title})
		state.mu.Unlock()
		return Created{ID: id, Title: c.Title}, nil
	}), commands.WithPath[CreateTask]("/api/create-task")); err != nil {
		return nil, err
	}
	if err := commands.Register[CompleteTask](b, commands.Handle(func(c CompleteTask, ctx context.Context) (Task, error) {
		if err := ctx.Err(); err != nil {
			return Task{}, err
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		for i := range state.tasks {
			if state.tasks[i].ID == c.TaskID {
				state.tasks[i].Completed = true
				return state.tasks[i], nil
			}
		}
		return Task{}, fmt.Errorf("task not found")
	}), commands.WithPath[CompleteTask]("/api/complete-task")); err != nil {
		return nil, err
	}
	if err := queries.Register[Task](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]Task, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		return append([]Task{}, state.tasks...), nil
	}), queries.WithPath[queries.NoArguments]("/api/tasks")); err != nil {
		return nil, err
	}
	if err := queries.Register[Task](b, "ByID", queries.Function(func(_ context.Context, args ByID) (*Task, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		for _, task := range state.tasks {
			if task.ID == args.ID {
				copy := task
				return &copy, nil
			}
		}
		return nil, nil
	}), queries.WithPath[ByID]("/api/tasks/by-id")); err != nil {
		return nil, err
	}
	return b.Build()
}
