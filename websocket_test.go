// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

const wsHub = "/.cratis/queries/ws"

func TestDirectWebSocketDataFragmentedPingAndOrderlyCompletion(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: "first"}, observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	_, server := startSSEServer(t, b, false)
	client, response := openWireWS(t, server, "/observe", http.Header{"Accept": {"text/event-stream"}})
	if response.StatusCode != 101 {
		t.Fatal(response.StatusCode)
	}
	message := client.message(t)
	if message["type"] != "Data" || message["data"].(map[string]any)["data"].(map[string]any)["name"] != "first" || message["queryId"] != nil || message["payload"] != nil {
		t.Fatal(message)
	}
	client.frame(t, 1, false, []byte(`{"type":"Ping",`))
	client.frame(t, 0, true, []byte(`"timestamp":1700000000123}`))
	message = client.message(t)
	if message["type"] != "Pong" || message["timestamp"] != float64(1700000000123) {
		t.Fatal(message)
	}
	if err := state.Complete(); err != nil {
		t.Fatal(err)
	}
	if message, err := client.read(t); err != io.EOF {
		t.Fatal(message, err)
	}
}

func TestDirectWebSocketLiveDenialIsFinalDataResult(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: "first"}, observable.SubjectOptions[builderModel]{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	var emitted atomic.Int32
	if err := b.Queries().AddEmissionGuard("test", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(context.Context, queries.EmissionContext) (queries.EmissionVerdict, error) {
			if emitted.Add(1) == 1 {
				return queries.Allow, nil
			}
			return queries.DenyAndTerminate, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	client, response := openWireWS(t, server, "/observe", nil)
	if response.StatusCode != 101 {
		t.Fatal(response.StatusCode)
	}
	if message := client.message(t); message["type"] != "Data" {
		t.Fatal(message)
	}
	if err := state.Publish(t.Context(), builderModel{Name: "denied"}); err != nil {
		t.Fatal(err)
	}
	message := client.message(t)
	if message["type"] != "Data" || message["data"].(map[string]any)["isAuthorized"] != false {
		t.Fatal(message)
	}
	if message, err := client.read(t); err != io.EOF {
		t.Fatal(message, err)
	}
	// A socket denial never poisons the shared application source.
	if err := state.Publish(t.Context(), builderModel{Name: "other consumer"}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectWebSocketPreUpgradeDenialOriginAndHEADDoNotActivate(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[builderModel], error) {
		calls.Add(1)
		return nil, nil
	}), queries.WithPath[queries.NoArguments]("/observe"), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{})); err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	_, response := openWireWS(t, server, "/observe", nil)
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	_, response = openWireWS(t, server, "/observe", http.Header{"Origin": {"https://evil.invalid"}})
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), "HEAD", server.URL+wsHub, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || calls.Load() != 0 {
		t.Fatal(response.StatusCode, calls.Load())
	}
}

func TestWebSocketHubMultiplexingRevisionsAndIndependentFailure(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: "first"}, observable.SubjectOptions[builderModel]{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	registerHubState(t, b, state, &calls)
	a, server := startSSEServer(t, b, false)
	client, response := openWireWS(t, server, wsHub, nil)
	if response.StatusCode != 101 {
		t.Fatal(response.StatusCode)
	}
	connected := client.message(t)
	if connected["type"] != "Connected" || connected["supportsSubscriptionRevisions"] != true || connected["keepAliveIntervalMs"] != float64(30000) {
		t.Fatal(connected)
	}
	request := hubQueryRequest(a)
	client.send(t, map[string]any{"type": "Subscribe", "queryId": "q1", "payload": request})
	message := client.message(t)
	if message["type"] != "QueryResult" || message["queryId"] != "q1" || message["revision"] != nil {
		t.Fatal(message)
	}
	client.send(t, map[string]any{"type": "Subscribe", "queryId": "q1", "revision": 1, "payload": request})
	message = client.message(t)
	if message["revision"] != float64(1) {
		t.Fatal(message)
	}
	client.send(t, map[string]any{"type": "Subscribe", "queryId": "q2", "revision": 2, "payload": request})
	message = client.message(t)
	if message["queryId"] != "q2" {
		t.Fatal(message)
	}
	client.send(t, map[string]any{"type": "Subscribe", "queryId": "bad", "revision": 3, "payload": map[string]any{"queryName": "Unknown.Query", "transferMode": "full"}})
	message = client.message(t)
	if message["type"] != "Error" || message["queryId"] != "bad" {
		t.Fatal(message)
	}
	client.send(t, map[string]any{"type": "Unsubscribe", "queryId": "q1", "revision": 1})
	client.send(t, map[string]any{"type": "Subscribe", "queryId": "q1", "revision": 1, "payload": request})
	client.send(t, map[string]any{"type": "Subscribe", "queryId": "q1", "payload": request})
	// Pong is an ordered reader barrier, not a sleep. All preceding stale
	// controls have been parsed by the time it is delivered.
	client.send(t, map[string]any{"type": "Ping", "timestamp": 5})
	message = client.message(t)
	if message["type"] != "Pong" || calls.Load() != 3 {
		t.Fatal(message, calls.Load())
	}
	if err := state.Publish(t.Context(), builderModel{Name: "next"}); err != nil {
		t.Fatal(err)
	}
	message = client.message(t)
	if message["queryId"] != "q2" || message["payload"].(map[string]any)["data"].(map[string]any)["name"] != "next" {
		t.Fatal(message)
	}
}

func TestWebSocketLiveDenialTerminalAndShutdownJoinsHijackedSocket(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: "first"}, observable.SubjectOptions[builderModel]{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	var emissions atomic.Int32
	if err := b.Queries().AddEmissionGuard("test", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(context.Context, queries.EmissionContext) (queries.EmissionVerdict, error) {
			if emissions.Add(1) == 1 {
				return queries.Allow, nil
			}
			return queries.DenyAndTerminate, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	a, server := startSSEServer(t, b, false)
	client, response := openWireWS(t, server, wsHub, nil)
	if response.StatusCode != 101 {
		t.Fatal(response.StatusCode)
	}
	_ = client.message(t)
	client.send(t, map[string]any{"type": "Subscribe", "queryId": "q1", "revision": 1, "payload": hubQueryRequest(a)})
	if message := client.message(t); message["type"] != "QueryResult" {
		t.Fatal(message)
	}
	if err := state.Publish(t.Context(), builderModel{Name: "denied"}); err != nil {
		t.Fatal(err)
	}
	if message := client.message(t); message["type"] != "Unauthorized" || message["revision"] != float64(1) {
		t.Fatal(message)
	}
	if err := state.Publish(t.Context(), builderModel{Name: "never"}); err != nil {
		t.Fatal(err)
	}
	client.send(t, map[string]any{"type": "Ping", "timestamp": 9})
	if message := client.message(t); message["type"] != "Pong" {
		t.Fatal(message)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if message, err := client.read(t); err == nil {
		t.Fatal("socket survived shutdown", message)
	}
}
