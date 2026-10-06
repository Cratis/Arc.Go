//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/identity"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/observation"
	"github.com/cratis/chronicle.go/reactors"
)

func TestReactorCommandRedeliveryRetainsCommitsAndWaitsForCompletion(t *testing.T) {
	id, err := concepts.NewUUID()
	require(t, err)
	// Keep the composed store+namespace database name within MongoDB's 63 bytes.
	storeName := chronicle.StoreName("arc-rd-" + id.String())
	const namespace chronicle.Namespace = "TenantA"
	const source = "request"
	state := newRedeliveryState()
	principal := identity.System("automation")
	bridge, err := integration.NewReactorCommands(integration.ReactorCommandOptions{
		Store: integration.StoreName(storeName), Principal: &principal, Replay: integration.LiveOnly})
	require(t, err)
	effects, err := sdk.CommandEffects(bridge, reflect.TypeFor[redeliveryCommand]())
	require(t, err)
	client, _, ctx := clientFor(t, func(registry *chronicle.Registry) {
		_, err := chronicle.RegisterEvent[RedeliveryRequested](registry)
		require(t, err)
		_, err = chronicle.RegisterEvent[RedeliveryRecorded](registry)
		require(t, err)
		require(t, chronicle.RegisterReactorSideEffectHandler(registry, effects))
		require(t, chronicle.RegisterReactor[*redeliveryReactor](registry, func() *redeliveryReactor {
			state.opened.Add(1)
			return &redeliveryReactor{state: state}
		}, reactors.WithID(redeliveryObserver), reactors.OnceOnly()))
	}, storeName)
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: storeName})
	require(t, err)
	require(t, adapter.Install(builder))
	require(t, commands.Register[redeliveryCommand](builder, commands.Handle(state.handle), commands.WithNoResponse[redeliveryCommand]()))
	app, err := builder.Build()
	require(t, err)
	require(t, bridge.Bind(app))
	require(t, app.Start(ctx))
	var failOnce, recoverOnce sync.Once
	fail := func() { failOnce.Do(func() { close(state.fail) }) }
	recoverDelivery := func() { recoverOnce.Do(func() { close(state.recover) }) }
	var store *chronicle.EventStore
	var joined bool
	join := func() {
		t.Helper()
		if joined {
			return
		}
		fail()
		recoverDelivery()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Stop/join the observer producer before closing its borrowed Arc target.
		if store != nil {
			if err := store.UnregisterReactor(cleanup, redeliveryObserver); err != nil {
				t.Error("reactor did not join", err)
				return
			}
		}
		if err := app.Shutdown(cleanup); err != nil {
			t.Error("Arc did not drain", err)
			return
		}
		if err := adapter.Close(); err != nil {
			t.Error("borrowed adapter close", err)
		}
		joined = true
		if state.active.Load() != 0 || state.opened.Load() != state.closed.Load() {
			t.Error("reactor/command resources remained after join", state.active.Load(), state.opened.Load(), state.closed.Load())
		}
	}
	t.Cleanup(join)
	store, err = client.EventStore(ctx, storeName, chronicle.WithNamespace(namespace))
	require(t, err)
	request, err := store.EventLog().Append(ctx, source, RedeliveryRequested{Name: "Ada"})
	require(t, err)
	require(t, request.Err())
	if request.Position == nil {
		t.Fatal("request append omitted position")
	}
	awaitRedeliveryGate(t, ctx, state, 1)
	assertNotAcknowledged := func() {
		t.Helper()
		info, err := store.Observers().Get(ctx, redeliveryObserver, events.EventLog)
		require(t, err)
		if info == nil || !info.IsSubscribed() || info.LastHandled() != events.Unavailable {
			t.Fatalf("blocked input must remain unacknowledged, observer = %+v", info)
		}
	}
	assertNotAcknowledged()
	if records := history(t, ctx, client, storeName, namespace, source); len(records) != 2 {
		t.Fatalf("prefix must commit before the later command completes: %d records", len(records))
	}
	fail()
	awaitRedeliveryState(t, ctx, "failed partition recorded", func() bool {
		partitions, err := store.Observers().FailedPartitions(ctx, redeliveryObserver)
		require(t, err)
		for _, partition := range partitions {
			if partition.Partition() == source && !partition.IsResolved() {
				attempts := partition.Attempts()
				if len(attempts) == 0 || attempts[0].Position() != *request.Position {
					t.Fatal("failure did not identify original delivery", attempts)
				}
				return true
			}
		}
		return false
	})
	calls, applied := state.snapshot()
	for _, call := range calls {
		if call.Step == "suffix" {
			t.Fatal("command after rejection ran before recovery")
		}
	}
	if applied != 1 {
		t.Fatalf("application effect count = %d, want 1", applied)
	}
	// This single explicit recovery mutation targets only our unique test store.
	// Accepted recovery is not completion; the following gate and snapshots prove it.
	outcome, err := store.Observers().RetryPartition(ctx, redeliveryObserver, events.EventLog, source)
	require(t, err)
	if outcome != observation.RecoveryStarted {
		t.Fatal("recovery refused", outcome)
	}
	awaitRedeliveryGate(t, ctx, state, 2)
	assertNotAcknowledged()
	calls, applied = state.snapshot()
	if len(calls) != 6 || applied != 1 {
		t.Fatalf("redelivery must rerun prefix/idempotent/gate, applying the app effect once: calls=%d effects=%d", len(calls), applied)
	}
	for index := range 3 {
		if calls[index].Delivery != calls[index+3].Delivery {
			t.Fatal("redelivery changed structured delivery identity")
		}
	}
	if records := history(t, ctx, client, storeName, namespace, source); len(records) != 3 {
		t.Fatalf("earlier committed prefix must remain and repeat without framework deduplication: %d records", len(records))
	}
	recoverDelivery()
	// Kernel 19.29.4 MongoDB storage deletes resolved failure records. Their
	// absence alone is not completion evidence; also require progress and output.
	awaitRedeliveryState(t, ctx, "recovered failure removed", func() bool {
		partitions, err := store.Observers().FailedPartitions(ctx, redeliveryObserver)
		require(t, err)
		return len(partitions) == 0
	})
	awaitRedeliveryState(t, ctx, "observer acknowledged recovered input", func() bool {
		info, err := store.Observers().Get(ctx, redeliveryObserver, events.EventLog)
		require(t, err)
		return info != nil && info.LastHandled() != events.Unavailable && info.LastHandled() >= *request.Position
	})
	join()
	if state.opened.Load() < 2 {
		t.Fatal("original and recovered deliveries did not open reactor resources")
	}
	records := history(t, ctx, client, storeName, namespace, source)
	if len(records) != 4 {
		t.Fatalf("history count = %d, want request, prefix, prefix, suffix", len(records))
	}
	var steps []string
	for _, record := range records[1:] {
		if record.Context.EventType.ID != "RedeliveryRecorded" || record.Context.Namespace != namespace ||
			record.Context.Store != storeName || record.Context.SourceID != source ||
			record.Context.CorrelationID != request.CorrelationID || record.Context.CausedBy.Subject != "[System]" {
			t.Fatal("committed output lost coordinates or trusted metadata", record.Context)
		}
		var value RedeliveryRecorded
		require(t, json.Unmarshal(record.Content, &value))
		steps = append(steps, value.Step)
	}
	if !slices.Equal(steps, []string{"prefix", "prefix", "suffix"}) {
		t.Fatal("unexpected committed command order", steps)
	}
	calls, applied = state.snapshot()
	if len(calls) != 7 || calls[6].Step != "suffix" || applied != 1 {
		t.Fatalf("unexpected final commands/effects: %+v / %d", calls, applied)
	}
	if other := history(t, ctx, client, storeName, "Default", source); len(other) != 0 {
		t.Fatal("reactor command escaped its tenant namespace", other)
	}
}
