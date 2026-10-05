// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package observability_test

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/cratis/arc.go/observability"
)

func recorder(t *testing.T, options observability.Options) *observability.Recorder {
	t.Helper()
	r, err := observability.NewRecorder(options)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecorderBoundsLabelsEventsAndCumulativeMetricsConcurrently(t *testing.T) {
	r := recorder(t, observability.Options{ArtifactLimit: 2, EventCapacity: 3})
	r.Register([]string{"Z", "B", "A"})
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for i := range 1000 {
				r.Register([]string{fmt.Sprintf("rejected-%d-%d", worker, i)})
				r.Record(observability.Observation{Artifact: fmt.Sprintf("SECRET-%d-%d", worker, i), Seconds: 0.5})
			}
		})
	}
	workers.Wait()
	r.Record(observability.Observation{Artifact: "A"})
	r.Record(observability.Observation{Artifact: "B"})
	s := r.Snapshot()
	if len(s.Metrics) != 3 || len(s.Events) != 3 || s.Dropped != 7999 || r.Label("Z") != observability.Other {
		t.Fatalf("unexpected bounds: %+v", s)
	}
	var total uint64
	for _, metric := range s.Metrics {
		total += metric.Count
	}
	if total != 8002 {
		t.Fatal(total)
	}
	body, err := json.Marshal(s)
	if err != nil || strings.Contains(string(body), "SECRET") || strings.Contains(string(body), "rejected-") {
		t.Fatal(string(body), err)
	}
	s.Metrics[0].Artifact = "mutated"
	s.Events[0].Artifact = "mutated"
	if r.Snapshot().Metrics[0].Artifact == "mutated" || r.Snapshot().Events[0].Artifact == "mutated" {
		t.Fatal("snapshot aliases state")
	}
}

type poisonousError struct{}

func (poisonousError) Error() string { panic("must not format exporter errors") }

func TestDrainContainsErrorsPanicsAndAllowsReentry(t *testing.T) {
	r := recorder(t, observability.Options{})
	for range 3 {
		r.Record(observability.Observation{})
	}
	calls := 0
	report := r.Drain(1000, func(observability.Observation) error {
		calls++
		_ = r.Snapshot()
		r.Record(observability.Observation{})
		switch calls {
		case 1:
			return poisonousError{}
		case 2:
			panic(poisonousError{})
		default:
			return nil
		}
	})
	s := r.Snapshot()
	if report.Removed != 3 || report.Errors != 1 || report.Panics != 1 || s.ExportErrors != 1 || s.ExportPanics != 1 || len(s.Events) != 3 || s.Metrics[0].Count != 6 {
		t.Fatal(report, s)
	}
	if r.Drain(1, nil).Removed != 0 || r.Drain(0, func(observability.Observation) error { t.Fatal("called"); return nil }).Removed != 0 {
		t.Fatal("invalid drain consumed events")
	}
}

func TestBlockedDrainDoesNotHoldProducerLock(t *testing.T) {
	r := recorder(t, observability.Options{})
	r.Record(observability.Observation{})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		r.Drain(1, func(observability.Observation) error { close(entered); <-release; return nil })
	}()
	<-entered
	r.Record(observability.Observation{})
	if r.Snapshot().Metrics[0].Count != 2 {
		t.Fatal("lost metric")
	}
	close(release)
	<-done
}

func TestRecorderNormalizesUntrustedEnumsAndDurations(t *testing.T) {
	r := recorder(t, observability.Options{})
	for _, seconds := range []float64{-1, math.Inf(1), math.NaN()} {
		r.Record(observability.Observation{Artifact: "unknown", Operation: 255, Transport: 255, Phase: 255, Outcome: 255, Seconds: seconds})
	}
	metric := r.Snapshot().Metrics[0]
	if metric.Count != 3 || metric.Seconds != 0 || metric.Outcome != observability.Error || metric.Artifact != observability.Other {
		t.Fatal(metric)
	}
}

func TestRecorderRejectsUnboundedConfigurationAndZeroIsDisabled(t *testing.T) {
	for _, options := range []observability.Options{{ArtifactLimit: -1}, {ArtifactLimit: 1001}, {EventCapacity: -1}, {EventCapacity: 65537}} {
		if _, err := observability.NewRecorder(options); err == nil {
			t.Fatal(options)
		}
	}
	for _, r := range []*observability.Recorder{nil, {}} {
		r.Register([]string{"A"})
		r.Record(observability.Observation{Artifact: "A"})
		if len(r.Snapshot().Metrics) != 0 || r.Drain(1, func(observability.Observation) error { t.Fatal("called"); return nil }).Removed != 0 {
			t.Fatal("zero recorder active")
		}
	}
}
