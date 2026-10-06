//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cratis/arc.go/integrations/chronicle/examples/sharedmodel"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/readmodels"
	"google.golang.org/protobuf/encoding/protojson"
)

func diagnosticValue[T any](value *T) string {
	if value == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%v", *value)
}

func logInventorySetup(t *testing.T, store *chronicle.EventStore) {
	t.Helper()
	for _, projection := range store.Projections() {
		wire, err := protojson.Marshal(projection.KernelDefinition())
		t.Logf("%s inventory setup store=%s namespace=%s projection=%s model=%s schema=%s KernelDefinition=%s error=%v", time.Now().UTC().Format(time.RFC3339Nano), store.Name(), store.Namespace(), projection.Identifier(), projection.Model().Identifier(), projection.Model().Schema(), wire, err)
	}
}

func logInventoryAppend(t *testing.T, store *chronicle.EventStore, event string, result eventsequences.AppendResult, err error) {
	t.Helper()
	t.Logf("%s inventory append store=%s namespace=%s event=%s disposition=%d position=%s resultError=%v operationError=%v", time.Now().UTC().Format(time.RFC3339Nano), store.Name(), store.Namespace(), event, result.Disposition, diagnosticValue(result.Position), result.Err(), err)
}

// Diagnostics never repair state or replace the original failure. Each read has
// at most one second within a separate five-second budget, not the polling wait.
func diagnoseInventory(t *testing.T, parent context.Context, store *chronicle.EventStore, model readmodels.Model[sharedmodel.Inventory]) {
	t.Helper()
	defer func() {
		if failure := recover(); failure != nil {
			t.Logf("inventory diagnostic panic (original failure preserved): %v", failure)
		}
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	t.Logf("%s inventory failure diagnostics store=%s namespace=%s model=%s", time.Now().UTC().Format(time.RFC3339Nano), store.Name(), store.Namespace(), model.Identifier())
	readCtx, stop := context.WithTimeout(ctx, time.Second)
	raw, err := store.ReadModels().Get(readCtx, model.Identifier(), "item-1")
	stop()
	t.Logf("raw model exists=%t LastHandled=%s JSON=%s error=%v", raw.Exists, diagnosticValue(raw.LastHandled), raw.Value, err)
	if err == nil && raw.Exists {
		descriptor, ok := store.ReadModels().Catalog().LookupIdentifier(model.Identifier())
		if ok {
			value, decodeErr := descriptor.Unmarshal(raw.Value)
			t.Logf("same-document descriptor decode error=%v value=%+v", decodeErr, value)
			if decoded, valid := value.(*sharedmodel.Inventory); valid && decodeErr == nil {
				t.Logf("same-document descriptor note=%s", diagnosticValue(decoded.Note))
			}
		}
		var standard sharedmodel.Inventory
		decodeErr := json.Unmarshal(raw.Value, &standard)
		t.Logf("same-document standard JSON note=%s value=%+v error=%v", diagnosticValue(standard.Note), standard, decodeErr)
	}
	readCtx, stop = context.WithTimeout(ctx, time.Second)
	history, err := store.EventLog().ReadSource(readCtx, "item-1", eventsequences.SourceFilter{})
	stop()
	t.Logf("source history count=%d error=%v", len(history), err)
	for _, event := range history {
		generations, encodeErr := json.Marshal(event.GenerationalContent)
		t.Logf("source history context=%+v JSON=%s originalJSON=%s generations=%s encodeError=%v", event.Context, event.Content, event.OriginalContent, generations, encodeErr)
	}
	readCtx, stop = context.WithTimeout(ctx, time.Second)
	observers, err := store.Observers().List(readCtx)
	stop()
	t.Logf("observers count=%d error=%v; public snapshots do not expose catching-up partition sets", len(observers), err)
	for _, observer := range observers {
		t.Logf("observer id=%s sequence=%s type=%d next=%d LastHandled=%d tail=%d running=%d eventTypes=%+v", observer.ID(), observer.Sequence(), observer.Type(), observer.Next(), observer.LastHandled(), observer.Tail(), observer.RunningState(), observer.EventTypes())
		readCtx, stop = context.WithTimeout(ctx, 250*time.Millisecond)
		failures, failureErr := store.Observers().FailedPartitions(readCtx, observer.ID())
		stop()
		t.Logf("observer=%s failedPartitions=%d error=%v", observer.ID(), len(failures), failureErr)
		for _, failure := range failures {
			t.Logf("failed partition id=%s partition=%s resolved=%t quarantined=%t", failure.ID(), failure.Partition(), failure.IsResolved(), failure.IsQuarantined())
			for _, attempt := range failure.Attempts() {
				t.Logf("failure occurred=%s position=%d kind=%d messages=%q stack=%s", attempt.Occurred(), attempt.Position(), attempt.Kind(), attempt.Messages(), attempt.StackTrace())
			}
		}
	}
	readCtx, stop = context.WithTimeout(ctx, time.Second)
	jobs, err := store.Jobs().List(readCtx)
	stop()
	t.Logf("catchup/replay jobs count=%d error=%v (absence does not prove completion)", len(jobs), err)
	for _, job := range jobs {
		t.Logf("job id=%s type=%s details=%s status=%d created=%s progress=%+v changes=%+v", job.ID(), job.Type(), job.Details(), job.Status(), job.Created(), job.Progress(), job.StatusChanges())
	}
	t.Logf("%s inventory diagnostic budget error=%v", time.Now().UTC().Format(time.RFC3339Nano), ctx.Err())
}
