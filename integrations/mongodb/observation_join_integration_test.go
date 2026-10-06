//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"go.mongodb.org/mongo-driver/v2/event"
)

func TestLiveObservationShutdownWaitCancellationRetainsCursorJoinOwnership(t *testing.T) {
	f := liveProvider(t)
	insertRows(t, f.collection(t, f.a, "ObservedJoin"), authorRows(1, 2))
	w := liveWatcher(t, f, mongodb.WatcherOptions{})
	p := liveObservationPipeline[providerTask](t, f, w, "ObservedJoin", mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
	ctx := liveContext(t, f.a, "reader")
	params := paged(0, 2)
	d := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
	d.acknowledge(t, errLiveDeliveryDone)
	if !errors.Is(liveReceive(t, d.done), errLiveDeliveryDone) {
		t.Fatal("observation did not join")
	}
	liveClose(t, d.observation)
	if err := p.CloseObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.record.mu.Lock()
	f.record.started = func(ctx context.Context, command *event.CommandStartedEvent) {
		if command.CommandName == "killCursors" {
			once.Do(func() {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
	}
	f.record.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	wait, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Close(wait) }()
	liveReceive(t, entered)
	cancel()
	if err := liveReceive(t, done); !errors.Is(err, context.Canceled) || !errors.Is(err, observable.ErrJoinPending) {
		t.Fatal("canceled wait certified cursor joining", err)
	}
	// Client is still application-owned while cleanup is held. Admission stops.
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal("borrowed client disconnected before cursor join", err)
	}
	close(release)
	liveClose(t, w)
	if countCommands(f.record.snapshot(), "killCursors") != 1 {
		t.Fatal("continued close repeated disposal")
	}
	if err := f.client.Ping(t.Context(), nil); err != nil {
		t.Fatal("watcher disconnected borrowed client", err)
	}
}

func TestLiveObservationCursorCleanupTimeoutIsFailureNotSuccessfulClose(t *testing.T) {
	f := liveProvider(t)
	insertRows(t, f.collection(t, f.a, "ObservedCleanupTimeout"), authorRows(1, 2))
	w, err := mongodb.NewWatcher(t.Context(), f.client, mongodb.WatcherOptions{CleanupTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := w.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Error("expected retained cleanup failure", err)
		}
	})
	p := liveObservationPipeline[providerTask](t, f, w, "ObservedCleanupTimeout", mongodb.ApplicationOwned, newLiveAuthority(), nil, nil)
	ctx := liveContext(t, f.a, "reader")
	params := paged(0, 2)
	d := runLiveObservation[providerTask](t, ctx, p, params, queries.Full)
	compareLiveUnary[providerTask](t, ctx, p, params, liveReceive(t, d.results))
	d.acknowledge(t, errLiveDeliveryDone)
	if !errors.Is(liveReceive(t, d.done), errLiveDeliveryDone) {
		t.Fatal("observation join")
	}
	liveClose(t, d.observation)
	configureFailure(t, f, "killCursors", true)
	wait, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := w.Close(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cleanup timeout reported success", err)
	}
	if countCommands(f.record.snapshot(), "killCursors") != 1 {
		t.Fatal("cleanup timeout not exercised")
	}
	if err := w.Close(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("continued Close lost failed disposal", err)
	}
	if countCommands(f.record.snapshot(), "killCursors") != 1 {
		t.Fatal("failed cleanup effect retried")
	}
}
