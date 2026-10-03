// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func TestSSEHubLiveDenialTerminatesOnlyItsOwner(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewState(builderModel{Name: "first"}, observable.SubjectOptions[builderModel]{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	if err := queries.RegisterObservable[builderModel](b, "LiveDenied", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[builderModel], error) { return state, nil })); err != nil {
		t.Fatal(err)
	}
	var deny atomic.Bool
	if err := b.Queries().AddEmissionGuard("live", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
			if deny.Load() && strings.HasSuffix(string(c.Name()), ".LiveDenied") {
				return queries.DenyAndTerminate, nil
			}
			return queries.Allow, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	a, server := startSSEServer(t, b, false)
	c := openTestHub(t, server, nil)
	for _, query := range a.Catalog().Queries {
		id := "allow"
		if query.Name == "LiveDenied" {
			id = "deny"
		}
		c.control(t, "/subscribe", id, 1, map[string]any{"queryName": query.Identity(), "transferMode": "full"}, nil, 200)
		if message := readSSEResult(t, c.reader); message["type"] != "QueryResult" || message["queryId"] != id {
			t.Fatal(message)
		}
	}
	deny.Store(true)
	if err := state.Publish(t.Context(), builderModel{Name: "second"}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for range 2 {
		message := readSSEResult(t, c.reader)
		seen[message["queryId"].(string)] = message["type"].(string)
		if message["type"] == "Unauthorized" && message["payload"] != nil {
			t.Fatal(message)
		}
	}
	if seen["deny"] != "Unauthorized" || seen["allow"] != "QueryResult" {
		t.Fatal(seen)
	}
	if err := state.Publish(t.Context(), builderModel{Name: "third"}); err != nil {
		t.Fatal(err)
	}
	message := readSSEResult(t, c.reader)
	if message["queryId"] != "allow" || message["type"] != "QueryResult" || message["payload"].(map[string]any)["data"].(map[string]any)["name"] != "third" {
		t.Fatal("result behind termination", message)
	}
}
func TestSSEHubKeepAliveIsAnApplicationPingNotComment(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Observable: arc.ObservableOptions{KeepAliveInterval: 10 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	_, server := startSSEServer(t, b, false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", server.URL+sseHub, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	reader := bufio.NewReader(response.Body)
	message := readSSEResult(t, reader)
	if message["type"] != "Connected" || message["keepAliveIntervalMs"] != float64(10) {
		t.Fatal(message)
	}
	message = readSSEResult(t, reader)
	if message["type"] != "Ping" || message["timestamp"].(float64) <= 0 || message["payload"] != nil {
		t.Fatal(message)
	}
}
