//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"testing"
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

type Author struct {
	ID   string `json:"id" chronicle:"key"`
	Name string `json:"name"`
}
type ReadAuthor struct {
	ID integration.EventSourceID `json:"id"`
}

func TestProjectionBackedCommandInjectionAndTenantIsolation(t *testing.T) {
	for _, passive := range []bool{false, true} {
		t.Run(map[bool]string{false: "materialized", true: "passive"}[passive], func(t *testing.T) {
			var model readmodels.Model[Author]
			client, store, ctx := clientFor(t, func(registry *chronicle.Registry) {
				event, err := chronicle.RegisterEvent[AuthorCreated](registry)
				require(t, err)
				model, err = chronicle.RegisterReadModel[Author](registry)
				require(t, err)
				if passive {
					require(t, registry.AddProjection(projections.ModelBound(model, projections.FromEvent(event), projections.Passive())))
				} else {
					require(t, registry.AddProjection(projections.ModelBound(model, projections.FromEvent(event))))
				}
			})
			builder, err := arc.NewBuilder(arc.Options{})
			require(t, err)
			adapter, err := sdk.New(client, sdk.Config{Store: store})
			require(t, err)
			require(t, sdk.BindReadModel(adapter, model))
			require(t, adapter.Install(builder))
			require(t, queries.RegisterReadModel[Author](builder))
			require(t, commands.Register[CreateAuthor](builder, commands.Handle(func(c CreateAuthor, _ context.Context) (AuthorCreated, error) {
				return AuthorCreated{Name: c.Name}, nil
			}), commands.WithNoResponse[CreateAuthor]()))
			require(t, commands.Register[ReadAuthor](builder, commands.Prepare(func(ctx context.Context, inv *commands.Invocation, _ ReadAuthor) (commands.Preparation[Author], error) {
				value, err := commands.RequireReadModel[Author](ctx, inv)
				return commands.Provided(value), err
			}, func(ctx context.Context, inv *commands.Invocation, _ ReadAuthor, prepared Author) (string, error) {
				value, err := commands.RequireReadModel[Author](ctx, inv)
				if value != prepared {
					t.Error("stages did not share read")
				}
				return value.Name, err
			})))
			app, err := builder.Build()
			require(t, err)
			require(t, app.Start(ctx))
			t.Cleanup(func() { require(t, app.Shutdown(context.Background())) })
			for _, tenantName := range []string{"Default", "TenantB"} {
				tenant, err := tenancy.ParseID(tenantName)
				require(t, err)
				tenantContext := tenancy.WithTenant(ctx, tenant)
				result, err := app.Commands().Execute(tenantContext, CreateAuthor{ID: "same", Name: tenantName})
				require(t, err)
				if !result.IsSuccess() {
					t.Fatal(result)
				}
				handle, err := client.EventStore(ctx, store, chronicle.WithNamespace(chronicle.Namespace(tenantName)))
				require(t, err)
				deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
				ticker := time.NewTicker(25 * time.Millisecond)
				for {
					value, err := readmodels.For(handle.ReadModels(), model).Get(deadline, "same")
					require(t, err)
					if value.Exists && value.Value.Name == tenantName {
						break
					}
					select {
					case <-deadline.Done():
						t.Fatal("projection did not reach expected state")
					case <-ticker.C:
					}
				}
				ticker.Stop()
				cancel()
				name, err := commands.Execute[string](tenantContext, app.Commands(), ReadAuthor{ID: "same"})
				require(t, err)
				if value, present := name.Response(); !present || value != tenantName {
					t.Fatal(name)
				}
			}
		})
	}
}
