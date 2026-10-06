//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/queries"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
)

func configureFailure(t *testing.T, f *providerFixture, command string, block bool) int64 {
	t.Helper()
	data := bson.D{{Key: "failCommands", Value: bson.A{command}}, {Key: "appName", Value: "arc-provider-" + osOwner()}}
	if block {
		data = append(data, bson.E{Key: "blockConnection", Value: true}, bson.E{Key: "blockTimeMS", Value: 1000})
	} else {
		// BadValue is not retryable. The application client also disables retries.
		data = append(data, bson.E{Key: "errorCode", Value: int32(2)})
	}
	var response struct {
		Count int64 `bson:"count"`
	}
	err := f.client.Database("admin").RunCommand(t.Context(), bson.D{{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: bson.D{{Key: "times", Value: 1}}}, {Key: "data", Value: data}}).Decode(&response)
	if err != nil {
		t.Fatalf("required MongoDB 8.0.15 failpoint profile unsupported: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 3*time.Second)
		defer cancel()
		if err := f.client.Database("admin").RunCommand(ctx, bson.D{{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: "off"}}).Err(); err != nil {
			t.Errorf("required provider failpoint cleanup: %v", err)
		}
	})
	return response.Count
}

func TestLiveCountFindAndGetMoreFailuresRetractWholeHTTPResult(t *testing.T) {
	f := liveProvider(t)
	insertRows(t, f.collection(t, f.a, "Faults"), authorRows(1, 150))
	app := registeredProvider[snapshot.Author](t, f, "Faults", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter}, activeFilter(), nil, nil)
	for _, command := range []string{"aggregate", "find", "getMore"} {
		t.Run(command, func(t *testing.T) {
			configureFailure(t, f, command, false)
			f.record.reset()
			response := providerHTTP(t, app, f.a, "/rows?page=0&pageSize=100", true)
			if response.Code != 500 || countCommands(f.record.snapshot(), command) != 1 {
				t.Fatal("required live fault not exercised", command, response.Code, response.Body.String())
			}
			assertProviderEnvelope(t, response, false)
			if command == "aggregate" && countCommands(f.record.snapshot(), "find") != 0 {
				t.Fatal("find ran after count failure")
			}
			if command == "getMore" && countCommands(f.record.snapshot(), "killCursors") != 1 {
				t.Fatal("failed cursor not closed", f.record.snapshot())
			}
		})
	}
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestLiveCanceledHTTPRequestClosesAcquiredCursorAndRetractsData(t *testing.T) {
	f := liveProvider(t)
	insertRows(t, f.collection(t, f.a, "Cancel"), authorRows(1, 150))
	app := registeredProvider[snapshot.Author](t, f, "Cancel", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter}, activeFilter(), nil, nil)
	entered := configureFailure(t, f, "getMore", true)
	started := make(chan struct{}, 1)
	f.record.mu.Lock()
	f.record.started = func(_ context.Context, command *event.CommandStartedEvent) {
		if command.CommandName == "getMore" {
			started <- struct{}{}
		}
	}
	f.record.mu.Unlock()
	f.record.reset()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/rows?page=0&pageSize=100", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer provider-fixture-reader")
	request.Header.Set("x-cratis-tenant-id", f.a.String())
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(response, request) }()
	// Always cancel/join the request, even if failpoint synchronization fails.
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("canceled request did not join")
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("getMore never started")
	}
	wait, stop := context.WithTimeout(t.Context(), 3*time.Second)
	err := f.client.Database("admin").RunCommand(wait, bson.D{{Key: "waitForFailPoint", Value: "failCommand"}, {Key: "timesEntered", Value: entered + 1}}).Err()
	stop()
	if err != nil {
		t.Fatalf("required cancellation failpoint was not entered: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled HTTP request did not join")
	}
	if response.Code != 500 {
		t.Fatal("canceled request succeeded", response.Code, response.Body.String())
	}
	// Arc refuses both ordinary and fallback encoding with a canceled
	// request context (ingress.go publish); no body is the existing contract.
	if response.Body.Len() != 0 {
		t.Fatal("canceled request published a body", response.Body.String())
	}
	if countCommands(f.record.snapshot(), "killCursors") != 1 {
		t.Fatal("cancel did not close acquired cursor", f.record.snapshot())
	}
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal("cancellation poisoned borrowed client", err)
	}
}

func TestLiveMandatoryRowAuthorizationRejectsMissingNilAndErrorBeforeIO(t *testing.T) {
	f := liveProvider(t)
	collection, err := mongodb.NewCollection[snapshot.Author](f.client, mongodb.CollectionOptions{Database: f.base, Name: "RowAuthorization", Ownership: mongodb.ApplicationOwned})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mongodb.NewRenderer(collection, mongodb.RendererOptions[snapshot.Author]{}); !errors.Is(err, mongodb.ErrConfiguration) {
		t.Fatal("missing RowFilter accepted", err)
	}
	failure := errors.New("private row predicate failure")
	for _, mode := range []string{"nil", "error"} {
		t.Run(mode, func(t *testing.T) {
			app := registeredProvider[snapshot.Author](t, f, "RowAuthorization", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: func(context.Context, queries.QueryContext) (bson.D, error) {
				if mode == "error" {
					return nil, failure
				}
				return nil, nil
			}}, nil, nil, nil)
			f.record.reset()
			result, err := typedSnapshot[snapshot.Author](t, app, f.a, paged(0, 2))
			assertNoProviderPublication(t, result, err)
			if len(f.record.snapshot()) != 0 {
				t.Fatal("rejected RowFilter performed I/O")
			}
			if mode == "error" && !errors.Is(err, failure) {
				t.Fatal("lost local row failure", err)
			}
		})
	}
}
