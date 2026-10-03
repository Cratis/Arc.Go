// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package clientfixture_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cratis/arc.go/ContractTests/observables/clientfixture"
)

func TestGeneratedRegistrationSuppressesFactoriesAndResolvesOnlyAcceptedSources(t *testing.T) {
	fixture, err := clientfixture.NewMode(true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fixture.Signals(); got != (clientfixture.Signals{}) {
		t.Fatalf("registration/build activated dependencies: %+v", got)
	}
	if err := fixture.App.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.App.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, path := range []string{"/items", "/api/contracts/items/private?group=alpha"} {
		response := httptest.NewRecorder()
		fixture.App.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		var result struct {
			IsSuccess bool `json:"isSuccess"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("%s: %v %s", path, err, response.Body.String())
		}
		if result.IsSuccess {
			t.Fatalf("rejected request succeeded: %s %s", path, response.Body.String())
		}
		got := fixture.Signals()
		if got.Resolve != 0 || got.Factory != 0 || got.Open != 0 {
			t.Fatalf("rejected request activated source: %s %+v", path, got)
		}
	}
	response := httptest.NewRecorder()
	fixture.App.ServeHTTP(response, httptest.NewRequest("GET", "/items?group=alpha", nil))
	if response.Code != 200 {
		t.Fatalf("valid generated snapshot: %d %s", response.Code, response.Body.String())
	}
	got := fixture.Signals()
	if got.Resolve != 1 || got.Factory != 1 || got.Open != 1 || got.Close != 1 {
		t.Fatalf("valid snapshot lifecycle: %+v", got)
	}
}

func TestGeneratedHostReadinessSnapshotsAndJoinedShutdown(t *testing.T) { hostedFixture(t, true) }

func TestHostedFixtureReadinessSnapshotsAndJoinedShutdown(t *testing.T) { hostedFixture(t, false) }

func hostedFixture(t *testing.T, generated bool) {
	t.Helper()
	fixture, err := clientfixture.NewMode(generated)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- fixture.App.Serve(ctx, listener) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(7 * time.Second):
				t.Error("fixture Serve did not join")
			}
		}
	})
	client := &http.Client{Timeout: 4 * time.Second}
	defer client.CloseIdleConnections()
	origin := "http://" + listener.Addr().String()
	get := func(path string, want int) map[string]any {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), "GET", origin+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != want {
			t.Fatalf("%s: status=%d want=%d", path, response.StatusCode, want)
		}
		var result map[string]any
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if get("/fixture/ready", 200)["All"] != fixture.Names["All"] {
		t.Fatal("missing actual registered identity")
	}
	for _, group := range []string{"nil", "pending"} {
		status := 200
		if group == "pending" {
			status = 202
		}
		result := get("/items?group="+group, status)
		if result["isReady"] != (group == "nil") {
			t.Fatalf("%s readiness: %v", group, result)
		}
		if _, present := result["data"]; present {
			t.Fatalf("%s invented data: %v", group, result)
		}
	}
	// The hosted shutdown must cancel a real pending SSE source and join it.
	request, err := http.NewRequestWithContext(t.Context(), "GET", origin+"/items?group=pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	stream, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Body.Close() }()
	if stream.StatusCode != 200 {
		t.Fatalf("pending SSE status=%d", stream.StatusCode)
	}
	waitRequest, err := http.NewRequestWithContext(t.Context(), "GET", origin+"/fixture/wait?active=1&group=pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitResponse, err := client.Do(waitRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = waitResponse.Body.Close()
	if waitResponse.StatusCode != 204 {
		t.Fatalf("pending SSE not activated: %d", waitResponse.StatusCode)
	}
	cancel()
	if body, err := io.ReadAll(stream.Body); err != nil || len(body) != 0 {
		t.Fatalf("pending shutdown invented data or failed: %q %v", body, err)
	}
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("fixture Serve did not join")
	}
	got := fixture.Signals()
	if got.Open != 3 || got.Close != 3 {
		t.Fatalf("shutdown source lifecycle: %+v", got)
	}
}
