// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

type hubResources struct{ closes *atomic.Int32 }

func (r hubResources) Close(context.Context) error { r.closes.Add(1); return nil }

func TestSSEHubRetainsFailedOpeningAndCapacityUntilShutdownJoin(t *testing.T) {
	joined := make(chan struct{})
	var closes, stops atomic.Int32
	b, err := arc.NewBuilder(arc.Options{Observable: arc.ObservableOptions{CloseGrace: 20 * time.Millisecond, MaxSubscriptions: 1}, OpenResources: func(context.Context) (execution.Resources, error) { return hubResources{&closes}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	source := appObservableSource(func(context.Context) (observable.Stream[builderModel], error) {
		return appObservableStream{close: func(ctx context.Context) error {
			select {
			case <-joined:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, errors.New("private source failure")
	})
	registerHubState(t, b, source, nil)
	if err := b.AddLifecycle("feed", hook{start: func(context.Context) error { return nil }, stop: func(context.Context) error { stops.Add(1); return nil }}); err != nil {
		t.Fatal(err)
	}
	a, server := startSSEServer(t, b, false)
	c := openTestHub(t, server, nil)
	c.control(t, "/subscribe", "q", 1, hubQueryRequest(a), nil, 200)
	if message := readSSEResult(t, c.reader); message["type"] != "Error" {
		t.Fatal(message)
	}
	// Failed Open returned nil, but its timed-out stream cleanup is still owned.
	c.control(t, "/subscribe", "q2", 1, hubQueryRequest(a), nil, 429)
	if closes.Load() != 0 || stops.Load() != 0 {
		t.Fatal("disposed before joining")
	}
	budget, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	err = a.Shutdown(budget)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || closes.Load() != 0 || stops.Load() != 0 {
		t.Fatal(err, closes.Load(), stops.Load())
	}
	close(joined)
	budget, cancel = context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(budget); err != nil {
		t.Fatal(err)
	}
	if closes.Load() != 1 || stops.Load() != 1 {
		t.Fatal(closes.Load(), stops.Load())
	}
}
func TestSSEHubPostScopeIsIndependentAndShutdownClosesTransport(t *testing.T) {
	type borrowedKey struct{}
	opened := make(chan context.Context, 1)
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), borrowedKey{}, "request-only")))
		})
	}); err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(ctx context.Context, _ queries.NoArguments) (observable.Source[builderModel], error) {
		opened <- ctx
		return state, nil
	})); err != nil {
		t.Fatal(err)
	}
	a, server := startSSEServer(t, b, false)
	c := openTestHub(t, server, nil)
	c.control(t, "/subscribe", "q", 1, hubQueryRequest(a), nil, 200)
	ctx := <-opened
	if ctx.Value(borrowedKey{}) != nil || ctx.Err() != nil {
		t.Fatal("borrowed POST context or canceled source", ctx.Err())
	}
	if err := state.Publish(t.Context(), builderModel{Name: "after POST returned"}); err != nil {
		t.Fatal(err)
	}
	if message := readSSEResult(t, c.reader); message["type"] != "QueryResult" {
		t.Fatal(message)
	}
	budget, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(budget); err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(c.reader); err != nil || len(data) != 0 {
		t.Fatal(string(data), err)
	}
	if err := state.Publish(t.Context(), builderModel{Name: "shared feed remains alive"}); err != nil {
		t.Fatal(err)
	}
}
func TestSSEHubPhysicalConnectionAndIDLimitsRejectWithoutEviction(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Observable: arc.ObservableOptions{MaxConnections: 1, MaxQueryIDs: 1}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	registerHubState(t, b, state, nil)
	a, server := startSSEServer(t, b, false)
	c := openTestHub(t, server, nil)
	r, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+sseHub, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 429 {
		t.Fatal(response.StatusCode)
	}
	c.control(t, "/unsubscribe", "q", 1, nil, nil, 200)
	c.control(t, "/subscribe", "q2", 1, hubQueryRequest(a), nil, 429)
	c.control(t, "/subscribe", "q", 1, hubQueryRequest(a), nil, 200)
	c.control(t, "/subscribe", "q", 2, hubQueryRequest(a), nil, 200)
}
