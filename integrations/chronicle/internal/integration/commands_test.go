//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/constraints"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
)

type AuthorCreated struct {
	Name string `json:"name"`
}
type CreateAuthor struct {
	ID   integration.EventSourceID `json:"id"`
	Name string                    `json:"name"`
}
type CreatePair struct {
	ID integration.EventSourceID `json:"id"`
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func clientFor(t *testing.T, register func(*chronicle.Registry)) (*chronicle.Client, chronicle.StoreName, context.Context) {
	t.Helper()
	endpoint := os.Getenv("CHRONICLE_INTEGRATION_CONNECTION_STRING")
	if endpoint == "" {
		t.Fatal("CHRONICLE_INTEGRATION_CONNECTION_STRING is required with -tags=integration")
	}
	t.Log("kernel cratis/chronicle:19.29.4-development", os.Getenv("CHRONICLE_INTEGRATION_IMAGE_DIGEST"))
	registry := chronicle.NewRegistry()
	register(registry)
	client, err := chronicle.NewClient(chronicle.WithRegistry(registry), chronicle.WithConnectionString(endpoint), chronicle.WithDevelopmentDefaults())
	require(t, err)
	t.Cleanup(func() { require(t, client.Close()) })
	id, err := concepts.NewUUID()
	require(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	return client, chronicle.StoreName("arc-go-" + id.String()), ctx
}
func authorClient(t *testing.T) (*chronicle.Client, chronicle.StoreName, context.Context) {
	return clientFor(t, func(registry *chronicle.Registry) {
		event, err := chronicle.RegisterEvent[AuthorCreated](registry)
		require(t, err)
		constraint, err := constraints.UniqueValues("unique-author-name").On(event.Descriptor(), "name").Build()
		require(t, err)
		require(t, registry.AddConstraint(constraint))
	})
}
func authorApp(t *testing.T, client *chronicle.Client, store chronicle.StoreName) *arc.Application {
	t.Helper()
	builder, err := arc.NewBuilder(arc.Options{Namespace: "Authors"})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: store})
	require(t, err)
	require(t, adapter.Install(builder))
	require(t, commands.Register[CreateAuthor](builder, commands.Handle(func(c CreateAuthor, _ context.Context) (AuthorCreated, error) {
		return AuthorCreated{Name: c.Name}, nil
	}), commands.WithPath[CreateAuthor]("/create"), commands.WithNoResponse[CreateAuthor](), commands.WithValidator[CreateAuthor](validation.ValidatorFunc[CreateAuthor](func(_ context.Context, c CreateAuthor) ([]validation.Result, error) {
		if c.Name == "" {
			return []validation.Result{{Severity: validation.Error, Message: "Name is required."}}, nil
		}
		return nil, nil
	}))))
	require(t, commands.Register[CreatePair](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, c CreatePair) (integration.EventBatch, error) {
		result, err := inv.Pipeline().Execute(ctx, CreateAuthor{ID: c.ID, Name: "nested"})
		if err != nil {
			return integration.EventBatch{}, err
		}
		if !result.IsSuccess() {
			return integration.EventBatch{}, validation.Reject(result.Details().ValidationResults...)
		}
		return integration.Events(AuthorCreated{Name: "outer"}), nil
	}), commands.WithNoResponse[CreatePair]()))
	app, err := builder.Build()
	require(t, err)
	require(t, app.Start(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require(t, app.Shutdown(ctx))
	})
	return app
}
func history(t *testing.T, ctx context.Context, client *chronicle.Client, store chronicle.StoreName, namespace chronicle.Namespace, source string) []events.Appended {
	t.Helper()
	handle, err := client.EventStore(ctx, store, chronicle.WithNamespace(namespace))
	require(t, err)
	values, err := handle.EventLog().ReadSource(ctx, events.SourceID(source), eventsequences.SourceFilter{})
	require(t, err)
	return values
}
func TestHTTPCommandCommitInputRejectionAndUniqueConstraint(t *testing.T) {
	client, store, ctx := authorClient(t)
	app := authorApp(t, client, store)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(`{"id":"a","name":"Ada"}`)).WithContext(ctx))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if got := history(t, ctx, client, store, "Default", "a"); len(got) != 1 {
		t.Fatal(got)
	}
	invalid, err := app.Commands().Execute(ctx, CreateAuthor{ID: "invalid"})
	require(t, err)
	if invalid.IsSuccess() || len(history(t, ctx, client, store, "Default", "invalid")) != 0 {
		t.Fatal(invalid)
	}
	rejected, err := app.Commands().Execute(ctx, CreateAuthor{ID: "b", Name: "Ada"})
	if err == nil || rejected.IsSuccess() {
		t.Fatal(rejected, err)
	}
	findings := rejected.Details().ValidationResults
	if len(findings) == 0 || findings[0].Reason != validation.ConstraintViolation || len(findings[0].Members) == 0 || findings[0].Members[0] != "name" {
		t.Fatal(rejected.Details(), err)
	}
	if got := history(t, ctx, client, store, "Default", "b"); len(got) != 0 {
		t.Fatal("rejected event persisted", got)
	}
}
func TestNestedOrderingAndTenantIsolation(t *testing.T) {
	client, store, ctx := authorClient(t)
	app := authorApp(t, client, store)
	result, err := app.Commands().Execute(ctx, CreatePair{ID: "pair"})
	require(t, err)
	if !result.IsSuccess() {
		t.Fatal(result)
	}
	values := history(t, ctx, client, store, "Default", "pair")
	if len(values) != 2 {
		t.Fatal(values)
	}
	for index, name := range []string{"nested", "outer"} {
		var e AuthorCreated
		require(t, json.Unmarshal(values[index].Content, &e))
		if e.Name != name {
			t.Fatal(values)
		}
	}
	tenant, err := tenancy.ParseID("other")
	require(t, err)
	result, err = app.Commands().Execute(tenancy.WithTenant(ctx, tenant), CreateAuthor{ID: "pair", Name: "nested"})
	require(t, err)
	if !result.IsSuccess() || len(history(t, ctx, client, store, "other", "pair")) != 1 || len(history(t, ctx, client, store, "Default", "pair")) != 2 {
		t.Fatal(result)
	}
}
