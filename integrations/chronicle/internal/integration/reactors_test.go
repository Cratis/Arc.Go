//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
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
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/observation"
	"github.com/cratis/chronicle.go/reactors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcmetadata "google.golang.org/grpc/metadata"
)

type CreateRequested struct {
	Name string `json:"name"`
}
type CreateFromRequest struct{ deliveries *reactorDeliveries }

func (r *CreateFromRequest) Handle(event CreateRequested, ctx events.Context) CreateAuthor {
	r.deliveries.record(ctx.SourceID)
	return CreateAuthor{ID: integration.EventSourceID(ctx.SourceID), Name: event.Name}
}

// reactorDeliveries counts client-side reactor invocations per event source,
// separating a delivery the kernel never made from one Arc mishandled.
type reactorDeliveries struct {
	mu       sync.Mutex
	bySource map[events.SourceID]int
}

func (d *reactorDeliveries) record(source events.SourceID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bySource[source]++
}

func (d *reactorDeliveries) count(source events.SourceID) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bySource[source]
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
	deliveries := &reactorDeliveries{bySource: map[events.SourceID]int{}}
	client, _, ctx := clientFor(t, func(registry *chronicle.Registry) {
		_, err := chronicle.RegisterEvent[CreateRequested](registry)
		require(t, err)
		_, err = chronicle.RegisterEvent[AuthorCreated](registry)
		require(t, err)
		require(t, chronicle.RegisterReactorSideEffectHandler(registry, effects))
		require(t, chronicle.RegisterReactor[*CreateFromRequest](registry, func() *CreateFromRequest { return &CreateFromRequest{deliveries: deliveries} }, reactors.WithID("arc-create"), reactors.OnceOnly()))
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
	awaitReactorOutcome(t, ctx, ctx, store, deliveries, "success", appendResult.Position, "reactor command did not append", func(wait context.Context) (bool, error) {
		handle, err := client.EventStore(wait, storeName)
		if err != nil {
			return false, err
		}
		records, err := handle.EventLog().ReadSource(wait, "success", eventsequences.SourceFilter{})
		if err != nil || len(records) != 2 {
			return false, err
		}
		if records[1].Context.EventType.ID != "AuthorCreated" || records[1].Context.CorrelationID != appendResult.CorrelationID || records[1].Context.CausedBy.Subject != "[System]" {
			t.Fatal(records)
		}
		return true, nil
	})
	appendResult, err = store.EventLog().Append(ctx, "failed", CreateRequested{})
	require(t, err)
	require(t, appendResult.Err())
	failures, authenticated := failureClient(t, ctx)
	awaitReactorOutcome(t, ctx, authenticated, store, deliveries, "failed", appendResult.Position, "command rejection did not fail observer partition", func(wait context.Context) (bool, error) {
		response, err := failures.GetFailedPartitions(wait, &contracts.GetFailedPartitionsRequest{EventStore: string(storeName), Namespace: "Default", ObserverId: "arc-create"})
		if err != nil {
			return false, err
		}
		for _, partition := range response.Items {
			if partition.Partition == "failed" {
				return true, nil
			}
		}
		return false, nil
	})
	if len(history(t, ctx, client, storeName, "Default", "failed")) != 1 {
		t.Fatal("rejected reactor command appended output")
	}
}

// awaitReactorOutcome polls done for up to 15 seconds per phase. On timeout it
// separates the upstream kernel strand (Chronicle#4548: no delivery reached the
// client while the subscribed, active observer stays behind a known tail) from
// every other cause, which fails the test. done polls with a deadline derived
// from poll; the diagnosis reads the kernel with ctx.
func awaitReactorOutcome(t *testing.T, ctx, poll context.Context, store *chronicle.EventStore, deliveries *reactorDeliveries, source events.SourceID, position *events.SequenceNumber, failure string, done func(context.Context) (bool, error)) {
	t.Helper()
	if position == nil {
		t.Fatal("append omitted position")
	}
	wait, cancel := context.WithTimeoutCause(poll, 15*time.Second, errObserverWaitElapsed)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := done(wait)
		if ok {
			return
		}
		if err != nil && !readEndedByWindow(wait, err) {
			t.Fatal(failure, err)
		}
		select {
		case <-wait.Done():
			if err := poll.Err(); err != nil {
				t.Fatal("test context exhausted while waiting for reactor outcome:", err)
			}
			if err := ctx.Err(); err != nil {
				t.Fatal("test context exhausted while waiting for reactor outcome:", err)
			}
			diagnoseReactorStall(t, ctx, store, deliveries, source, *position, observerWaitElapsed(wait), failure)
			return
		case <-ticker.C:
		}
	}
}

func diagnoseReactorStall(t *testing.T, parent context.Context, store *chronicle.EventStore, deliveries *reactorDeliveries, source events.SourceID, position events.SequenceNumber, waitElapsed bool, failure string) {
	t.Helper()
	stall, evidence := observeStall(t, parent, store, "arc-create", events.EventLog, position, deliveries.count(source), waitElapsed, failure)
	evidence = fmt.Sprintf("source=%s %s", source, evidence)
	if stall.matchesChronicle4548() {
		knownKernelDefectObserved(t, chronicle4548+": reactor stranded behind the event-log tail; re-enable with "+reactorStrandIssue, failure, evidence)
		return
	}
	t.Fatal(failure, evidence)
}

// observeStall reads the kernel state of an observer that did not reach the
// event at position and fails the test when that state is unreadable. delivered
// is the client-side invocation count, zero for observers with no client hook.
// waitElapsed records whether the full observation window elapsed.
func observeStall(t *testing.T, parent context.Context, store *chronicle.EventStore, observerID observation.ID, sequence events.SequenceID, position events.SequenceNumber, delivered int, waitElapsed bool, failure string) (reactorStall, string) {
	t.Helper()
	read, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	info, err := store.Observers().Get(read, observerID, sequence)
	if err != nil || info == nil {
		t.Fatal(failure, "; observer unavailable:", err)
	}
	partitions, err := store.Observers().FailedPartitions(read, observerID)
	if err != nil {
		t.Fatal(failure, "; failed partitions unavailable:", err)
	}
	unresolved := 0
	for _, partition := range partitions {
		if !partition.IsResolved() {
			unresolved++
		}
	}
	stall := reactorStall{
		WaitElapsed: waitElapsed, Position: uint64(position), Delivered: delivered, UnresolvedFailures: unresolved,
		Active: info.RunningState() == observation.Active, Subscribed: info.IsSubscribed(),
		LastHandled: uint64(info.LastHandled()), Tail: uint64(info.Tail()),
	}
	return stall, fmt.Sprintf("observer=%s %+v next=%d handled=%d", observerID, stall, info.Next(), info.HandledEventCount())
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
