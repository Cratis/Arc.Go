// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"io"
	"testing"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func phaseCount(recorder *observability.Recorder, phase observability.Phase, transport observability.Transport) uint64 {
	var count uint64
	for _, metric := range recorder.Snapshot().Metrics {
		if metric.Phase == phase && metric.Transport == transport {
			count += metric.Count
		}
	}
	return count
}

//nolint:staticcheck // A nil context is an explicit rejection regression.
func TestObservableSnapshotRecordsOneLogicalAttemptPerCall(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, scenario := range []struct {
		name      string
		ctx       context.Context
		options   queries.PipelineOptions
		wantError bool
	}{
		{"nil context", nil, queries.PipelineOptions{}, true},
		{"cancelled before admission", cancelled, queries.PipelineOptions{}, true},
		{"tenant rejected before admission", t.Context(), queries.PipelineOptions{RequireTenant: true}, true},
		{"admitted snapshot", t.Context(), queries.PipelineOptions{}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			recorder := diagnosticRecorder(t)
			opened := 0
			state, err := observable.NewState(Item{}, observable.SubjectOptions[Item]{})
			mustRegister(t, err)
			source := sourceFunc[Item](func(ctx context.Context) (observable.Stream[Item], error) {
				opened++
				return state.Open(ctx)
			})
			scenario.options.Diagnostics = recorder
			p := observationPipeline(t, observableRegistry(t, source), scenario.options)
			_, err = p.Perform(scenario.ctx, "Item.Observe", queries.Request{})
			if (err != nil) != scenario.wantError {
				t.Fatal(err)
			}
			if scenario.wantError && opened != 0 {
				t.Fatal("source opened by a rejected snapshot")
			}
			if got := phaseCount(recorder, observability.Completed, observability.SnapshotTransport); got != 1 {
				t.Fatal("snapshot attempts", got, recorder.Snapshot())
			}
			if got := phaseCount(recorder, observability.Opening, observability.ObservableTransport); got != 0 {
				t.Fatal("snapshot recorded a second logical opening attempt", got, recorder.Snapshot())
			}
			if scenario.wantError && len(recorder.Snapshot().Events) != 1 {
				t.Fatal(recorder.Snapshot())
			}
		})
	}
}

func TestConcurrentCloseOnlySerializedCloserCertifiesJoinAfterAdmissionRelease(t *testing.T) {
	recorder := diagnosticRecorder(t)
	source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) {
		return streamFuncs[Item]{next: func(context.Context) (Item, error) { return Item{}, io.EOF }, close: func(context.Context) error { return nil }}, nil
	})
	p := observationPipeline(t, observableRegistry(t, source), queries.PipelineOptions{Diagnostics: recorder, EnableQueryHealth: true})
	entered, proceed, released := make(chan struct{}), make(chan struct{}), 0
	ctx, lease := boundary.WithObservationAdmission(t.Context(), func() {
		close(entered)
		<-proceed
		released++
	})
	o, _, err := p.Open(ctx, "Item.Observe", queries.Request{})
	if err != nil || o == nil {
		lease.ReleaseUnused()
		t.Fatal(o, err)
	}
	closeA := make(chan error, 1)
	go func() { closeA <- o.Close(t.Context()) }()
	<-entered // Close A owns the gate and is blocked releasing admission ownership.

	expired, cancel := context.WithCancel(t.Context())
	cancel()
	if err := o.Close(expired); !errors.Is(err, context.Canceled) {
		t.Fatal("competing close did not time out at the gate", err)
	}
	if got := phaseCount(recorder, observability.Joined, observability.ObservableTransport) + phaseCount(recorder, observability.Cleanup, observability.ObservableTransport); got != 0 {
		t.Fatal("join certified before admission release completed", recorder.Snapshot())
	}

	close(proceed)
	if err := <-closeA; err != nil || released != 1 {
		t.Fatal(err, released)
	}
	if diagnosticCount(recorder, observability.Joined, observability.Cancelled) != 1 || diagnosticCount(recorder, observability.Cleanup, observability.Cancelled) != 1 || phaseCount(recorder, observability.Joined, observability.ObservableTransport) != 1 {
		t.Fatal("expected exactly one authoritative join", recorder.Snapshot())
	}
	if err := o.Close(t.Context()); err != nil || released != 1 || phaseCount(recorder, observability.Joined, observability.ObservableTransport) != 1 {
		t.Fatal("repeat close must be idempotent", err, released, recorder.Snapshot())
	}
	if len(p.(queries.HealthReporter).QueryHealth()) != 0 {
		t.Fatal("closed observation must not be retained")
	}
}
