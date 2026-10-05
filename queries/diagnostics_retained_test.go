// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func TestDiagnosticsFailedOpeningRetainsHealthAndJoinsOnlyAfterResourceRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := diagnosticRecorder(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		joined := make(chan struct{})
		holder := &plainResources{}
		source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) {
			cancel()
			return streamFuncs[Item]{next: func(context.Context) (Item, error) { return Item{}, io.EOF }, close: func(ctx context.Context) error {
				select {
				case <-joined:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}, nil
		})
		p := observationPipeline(t, observableRegistry(t, source), queries.PipelineOptions{Diagnostics: recorder, EnableQueryHealth: true, CleanupTimeout: time.Second, MaxObservations: 1, OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
		o, result, err := p.Open(ctx, "Item.Observe", queries.Request{})
		if o != nil || !result.HasExceptions() || !errors.Is(err, context.DeadlineExceeded) || holder.closes != 0 {
			t.Fatal(o, result, err, holder.closes)
		}
		health := p.(queries.HealthReporter).QueryHealth()
		if len(health) != 1 || health[0].Retained != 1 {
			t.Fatal(health)
		}
		if diagnosticCount(recorder, observability.Opening, observability.Cancelled) != 1 {
			t.Fatal(recorder.Snapshot())
		}
		for _, event := range recorder.Snapshot().Events {
			if event.Phase == observability.Joined {
				t.Fatal("premature join", event)
			}
		}
		close(joined)
		mustRegister(t, p.CloseObservations(t.Context()))
		mustRegister(t, p.CloseObservations(t.Context()))
		if holder.closes != 1 || len(p.(queries.HealthReporter).QueryHealth()) != 0 || diagnosticCount(recorder, observability.Joined, observability.Cancelled) != 1 {
			t.Fatal(holder.closes, recorder.Snapshot())
		}
	})
}

func TestObservationDeliveryMetricsDoNotFillTheEventRing(t *testing.T) {
	recorder := diagnosticRecorder(t)
	next := 0
	source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) {
		return streamFuncs[Item]{next: func(context.Context) (Item, error) {
			next++
			if next > 2000 {
				return Item{}, io.EOF
			}
			return Item{}, nil
		}, close: func(context.Context) error { return nil }}, nil
	})
	p := observationPipeline(t, observableRegistry(t, source), queries.PipelineOptions{Diagnostics: recorder, EnableQueryHealth: true})
	mustRegister(t, queries.Subscribe[Item](t.Context(), p, "Item.Observe", queries.Request{}, func(queries.Result[Item]) error {
		// Sampling from a delivery callback must never deadlock with native locks.
		_ = p.(queries.HealthReporter).QueryHealth()
		return nil
	}))
	snapshot := recorder.Snapshot()
	if len(snapshot.Events) != 5 || snapshot.Dropped != 0 || diagnosticCount(recorder, observability.Delivered, observability.Success) != 2000 || diagnosticCount(recorder, observability.Opening, observability.Success) != 1 {
		t.Fatal(snapshot)
	}
}

//nolint:staticcheck // Nil context is an explicit rejection regression.
func TestSubscribeDiagnosticsCountTypedPreflightAndNilContextOnce(t *testing.T) {
	for _, scenario := range []string{"unknown", "mismatch", "nil"} {
		t.Run(scenario, func(t *testing.T) {
			recorder := diagnosticRecorder(t)
			state, err := observable.NewState(Item{}, observable.SubjectOptions[Item]{})
			mustRegister(t, err)
			p := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{Diagnostics: recorder})
			switch scenario {
			case "unknown":
				err = queries.Subscribe[Item](t.Context(), p, "SECRET unknown", queries.Request{}, func(queries.Result[Item]) error { return nil })
			case "mismatch":
				err = queries.Subscribe[string](t.Context(), p, "Item.Observe", queries.Request{}, func(queries.Result[string]) error { return nil })
			case "nil":
				err = queries.Subscribe[Item](nil, p, "Item.Observe", queries.Request{}, func(queries.Result[Item]) error { return nil })
			}
			if err == nil || len(recorder.Snapshot().Events) != 1 || diagnosticCount(recorder, observability.Opening, observability.Error) != 1 {
				t.Fatal(err, recorder.Snapshot())
			}
		})
	}
}
