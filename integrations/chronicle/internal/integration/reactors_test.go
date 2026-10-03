//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/identity"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
	contracts "github.com/cratis/chronicle.go/contracts/observation"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/reactors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcmetadata "google.golang.org/grpc/metadata"
)

type CreateRequested struct {
	Name string `json:"name"`
}
type CreateFromRequest struct{}

func (*CreateFromRequest) Handle(event CreateRequested, ctx events.Context) CreateAuthor {
	return CreateAuthor{ID: integration.EventSourceID(ctx.SourceID), Name: event.Name}
}

func TestReactorReturnedCommandCommitsBeforeAckAndFailureRecordsPartition(t *testing.T) {
	id, err := concepts.NewUUID()
	require(t, err)
	storeName := chronicle.StoreName("arc-reactor-" + id.String())
	principal := identity.System("automation")
	bridge, err := integration.NewReactorCommands(integration.ReactorCommandOptions{Store: integration.StoreName(storeName), Principal: &principal, Replay: integration.LiveOnly})
	require(t, err)
	effects, err := sdk.CommandEffects(bridge, reflect.TypeFor[CreateAuthor]())
	require(t, err)
	client, _, ctx := clientFor(t, func(registry *chronicle.Registry) {
		_, err := chronicle.RegisterEvent[CreateRequested](registry)
		require(t, err)
		_, err = chronicle.RegisterEvent[AuthorCreated](registry)
		require(t, err)
		require(t, chronicle.RegisterReactorSideEffectHandler(registry, effects))
		require(t, chronicle.RegisterReactor[*CreateFromRequest](registry, func() *CreateFromRequest { return &CreateFromRequest{} }, reactors.WithID("arc-create"), reactors.OnceOnly()))
	}, storeName)
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: storeName})
	require(t, err)
	require(t, adapter.Install(builder))
	require(t, commands.Register[CreateAuthor](builder, commands.Handle(func(c CreateAuthor, _ context.Context) (AuthorCreated, error) {
		return AuthorCreated{Name: c.Name}, nil
	}), commands.WithNoResponse[CreateAuthor](), commands.WithValidator[CreateAuthor](validation.ValidatorFunc[CreateAuthor](func(_ context.Context, c CreateAuthor) ([]validation.Result, error) {
		if c.Name == "" {
			return []validation.Result{{Severity: validation.Error, Message: "Name required"}}, nil
		}
		return nil, nil
	}))))
	app, err := builder.Build()
	require(t, err)
	require(t, bridge.Bind(app))
	require(t, app.Start(ctx))
	t.Cleanup(func() { require(t, app.Shutdown(context.Background())) })
	store, err := client.EventStore(ctx, storeName)
	require(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require(t, store.UnregisterReactor(cleanup, "arc-create"))
	})
	appendResult, err := store.EventLog().Append(ctx, "success", CreateRequested{Name: "Ada"})
	require(t, err)
	require(t, appendResult.Err())
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		records := history(t, wait, client, storeName, "Default", "success")
		if len(records) == 2 {
			if records[1].Context.EventType.ID != "AuthorCreated" || records[1].Context.CorrelationID != appendResult.CorrelationID || records[1].Context.CausedBy.Subject != "[System]" {
				t.Fatal(records)
			}
			break
		}
		select {
		case <-wait.Done():
			t.Fatal("reactor command did not append", wait.Err())
		case <-ticker.C:
		}
	}
	appendResult, err = store.EventLog().Append(ctx, "failed", CreateRequested{})
	require(t, err)
	require(t, appendResult.Err())
	failures, authenticated := failureClient(t, wait)
	for {
		response, err := failures.GetFailedPartitions(authenticated, &contracts.GetFailedPartitionsRequest{EventStore: string(storeName), Namespace: "Default", ObserverId: "arc-create"})
		require(t, err)
		found := false
		for _, partition := range response.Items {
			if partition.Partition == "failed" {
				found = true
			}
		}
		if found {
			break
		}
		select {
		case <-wait.Done():
			t.Fatal("command rejection did not fail observer partition", wait.Err())
		case <-ticker.C:
		}
	}
	if len(history(t, ctx, client, storeName, "Default", "failed")) != 1 {
		t.Fatal("rejected reactor command appended output")
	}
}

// failureClient uses the public kernel OAuth/contract APIs, not SDK internal test helpers.
func failureClient(t *testing.T, ctx context.Context) (contracts.FailedPartitionsClient, context.Context) {
	t.Helper()
	uri, err := chronicle.ParseConnectionString(os.Getenv("CHRONICLE_INTEGRATION_CONNECTION_STRING"))
	require(t, err)
	address := uri.Addresses()[0].String()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} // Test-owned development image only.
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {"chronicle-dev-client"}, "client_secret": {"chronicle-dev-secret"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address+"/connect/token", strings.NewReader(form.Encode()))
	require(t, err)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	require(t, err)
	defer func() { require(t, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		t.Fatal("test OAuth failed", response.StatusCode)
	}
	var token struct {
		Access string `json:"access_token"`
	}
	require(t, json.NewDecoder(response.Body).Decode(&token))
	if token.Access == "" {
		t.Fatal("empty test token")
	}
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)), grpc.WithDisableRetry())
	require(t, err)
	t.Cleanup(func() { require(t, connection.Close()) })
	return contracts.NewFailedPartitionsClient(connection), grpcmetadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token.Access)
}
