// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Command taskboard demonstrates returned events and a shared projection/query model.
// This example uses development credentials and TLS defaults; do not deploy unchanged.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/projections"
	"github.com/cratis/chronicle.go/readmodels"
)

// TaskCreated is a durable fact; its identity lives in the event context.
type TaskCreated struct {
	Title string `json:"title"`
}

// CreateTask is model-bound, with an ordinary typed Handle method.
type CreateTask struct {
	Title string `json:"title" validate:"required"`
}

func (c CreateTask) Handle(context.Context) (commands.Outcome[integration.EventSourceID], error) {
	id, err := integration.NewEventSourceID()
	if err != nil {
		return commands.Outcome[integration.EventSourceID]{}, err
	}
	return commands.Respond(id, TaskCreated(c)), nil
}

// Task is independently registered with Chronicle and Arc.
type Task struct {
	ID    string `json:"id" chronicle:"key"`
	Title string `json:"title"`
}
type ByID struct {
	ID string `json:"id"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() (err error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	endpoint := os.Getenv("CHRONICLE_INTEGRATION_CONNECTION_STRING")
	if endpoint == "" {
		endpoint = "chronicle://localhost:35000"
	}
	app, adapter, err := createApplication(endpoint, "taskboard")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, adapter.Close()) }()
	if err := app.Start(ctx); err != nil {
		return err
	}
	if err := adapter.Start(ctx); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return errors.Join(err, app.Shutdown(cleanup))
	}
	return app.Run(ctx, "127.0.0.1:8080")
}
func createApplication(endpoint string, storeName chronicle.StoreName) (app *arc.Application, adapter *integration.Integration, err error) {
	registry := chronicle.NewRegistry()
	event, err := chronicle.RegisterEvent[TaskCreated](registry)
	if err != nil {
		return nil, nil, err
	}
	model, err := chronicle.RegisterReadModel[Task](registry)
	if err != nil {
		return nil, nil, err
	}
	if err := registry.AddProjection(projections.ModelBound(model, projections.FromEvent(event), projections.Passive())); err != nil {
		return nil, nil, err
	}
	client, err := chronicle.NewClient(chronicle.WithRegistry(registry), chronicle.WithConnectionString(endpoint), chronicle.WithDevelopmentDefaults())
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, client.Close())
		}
	}()
	builder, err := arc.NewBuilder(arc.Options{Namespace: "TaskBoard"})
	if err != nil {
		return nil, nil, err
	}
	adapter, err = sdk.New(client, sdk.Config{Store: storeName, OwnClient: true, StartupNamespaces: []chronicle.Namespace{chronicle.DefaultNamespace}})
	if err != nil {
		return nil, nil, err
	}
	if err := sdk.BindReadModel(adapter, model); err != nil {
		return nil, nil, err
	}
	if err := adapter.Install(builder); err != nil {
		return nil, nil, err
	}
	if err := commands.Register[CreateTask](builder, commands.Handle(CreateTask.Handle), commands.WithPath[CreateTask]("/tasks/create")); err != nil {
		return nil, nil, err
	}
	if err := queries.Register[Task](builder, "ByID", queries.Function(func(ctx context.Context, args ByID) (Task, error) {
		tenant, _ := tenancy.TenantFrom(ctx)
		namespace := chronicle.DefaultNamespace
		if !tenant.IsDefault() {
			namespace = chronicle.Namespace(tenant.String())
		}
		store, err := client.EventStore(ctx, storeName, chronicle.WithNamespace(namespace))
		if err != nil {
			return Task{}, err
		}
		instance, err := readmodels.For(store.ReadModels(), model).Get(ctx, readmodels.Key(args.ID))
		if err != nil {
			return Task{}, err
		}
		if !instance.Exists {
			return Task{}, fmt.Errorf("task does not exist")
		}
		return instance.Value, nil
	}), queries.WithPath[ByID]("/tasks/by-id")); err != nil {
		return nil, nil, err
	}
	app, err = builder.Build()
	return app, adapter, err
}
