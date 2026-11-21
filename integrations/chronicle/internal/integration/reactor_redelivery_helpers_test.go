//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/identity"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/metadata"
	"github.com/cratis/chronicle.go/reactors"
)

// redeliveryCoordinates are the coordinates and correlation every committed
// reactor output must keep.
type redeliveryCoordinates struct {
	store         chronicle.StoreName
	namespace     chronicle.Namespace
	source        events.SourceID
	correlationID metadata.CorrelationID
}

func (c redeliveryCoordinates) keptBy(record events.Appended) bool {
	appended := record.Context
	sameLocation := appended.Store == c.store && appended.Namespace == c.namespace && appended.SourceID == c.source
	trusted := appended.CorrelationID == c.correlationID && appended.CausedBy.Subject == "[System]"
	return appended.EventType.ID == "RedeliveryRecorded" && sameLocation && trusted
}

// committedSteps checks that every reactor output after the request kept its
// coordinates and trusted metadata, and returns the recorded steps in order.
func committedSteps(t *testing.T, records []events.Appended, coordinates redeliveryCoordinates) []string {
	t.Helper()
	if len(records) == 0 {
		t.Fatal("history lost the request")
	}
	var steps []string
	for _, record := range records[1:] {
		if !coordinates.keptBy(record) {
			t.Fatal("committed output lost coordinates or trusted metadata", record.Context)
		}
		var value RedeliveryRecorded
		require(t, json.Unmarshal(record.Content, &value))
		steps = append(steps, value.Step)
	}
	return steps
}

const redeliveryObserver = "arc-redelivery"

type RedeliveryRequested struct {
	Name string `json:"name"`
}

type RedeliveryRecorded struct {
	Step string `json:"step"`
}

type redeliveryCommand struct {
	ID          integration.EventSourceID `json:"id"`
	Step        string                    `json:"step"`
	Delivery    reactors.Delivery         `json:"-"`
	Correlation correlation.ID            `json:"-"`
}

type redeliveryReactor struct{ state *redeliveryState }

func (*redeliveryReactor) Handle(_ RedeliveryRequested, event events.Context) []redeliveryCommand {
	delivery := reactors.Delivery{Reactor: redeliveryObserver, Store: event.Store, Namespace: event.Namespace,
		Sequence: event.Sequence, Partition: event.SourceID, SequenceNumber: event.SequenceNumber}
	var result []redeliveryCommand
	for _, step := range []string{"prefix", "idempotent", "gate", "suffix"} {
		result = append(result, redeliveryCommand{ID: integration.EventSourceID(event.SourceID), Step: step,
			Delivery: delivery, Correlation: correlation.ID(event.CorrelationID)})
	}
	return result
}

func (r *redeliveryReactor) Close() error {
	r.state.closed.Add(1)
	return nil
}

type redeliveryState struct {
	mu      sync.Mutex
	calls   []redeliveryCommand
	applied map[reactors.Delivery]bool
	effects int
	gates   int
	entered chan int
	fail    chan struct{}
	recover chan struct{}
	opened  atomic.Int32
	closed  atomic.Int32
	active  atomic.Int32
}

func newRedeliveryState() *redeliveryState {
	return &redeliveryState{applied: make(map[reactors.Delivery]bool), entered: make(chan int, 8),
		fail: make(chan struct{}), recover: make(chan struct{})}
}

func (s *redeliveryState) handle(command redeliveryCommand, ctx context.Context) (integration.EventBatch, error) {
	s.active.Add(1)
	defer s.active.Add(-1)
	principal, principalPresent := identity.PrincipalFrom(ctx)
	tenant, tenantPresent := tenancy.TenantFrom(ctx)
	if !principalPresent || !principal.Equal(identity.System("automation")) || !tenantPresent ||
		tenant.String() != string(command.Delivery.Namespace) || correlation.FromContext(ctx) != command.Correlation {
		return integration.EventBatch{}, errors.New("reactor command lost tenant, trusted actor or correlation")
	}
	s.mu.Lock()
	s.calls = append(s.calls, command)
	attempt := 0
	if command.Step == "gate" {
		s.gates++
		attempt = s.gates
	}
	if command.Step == "idempotent" && !s.applied[command.Delivery] {
		// This is a deliberately application-owned, in-process idempotent operation.
		// It atomically records a structured delivery key with its effect. It does
		// not claim persistence across process restart or framework deduplication.
		s.applied[command.Delivery] = true
		s.effects++
	}
	s.mu.Unlock()
	switch command.Step {
	case "prefix", "suffix":
		return integration.Events(RedeliveryRecorded{Step: command.Step}), nil
	case "idempotent":
		return integration.EventBatch{}, nil
	case "gate":
		select {
		case s.entered <- attempt:
		case <-ctx.Done():
			return integration.EventBatch{}, ctx.Err()
		}
		release := s.recover
		if attempt == 1 {
			release = s.fail
		}
		select {
		case <-release:
		case <-ctx.Done():
			return integration.EventBatch{}, ctx.Err()
		}
		if attempt == 1 {
			return integration.EventBatch{}, validation.Reject(validation.Result{Severity: validation.Error, Message: "deliberate first-delivery rejection"})
		}
		return integration.EventBatch{}, nil
	default:
		return integration.EventBatch{}, errors.New("unknown fixture command step")
	}
}

func (s *redeliveryState) snapshot() ([]redeliveryCommand, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]redeliveryCommand(nil), s.calls...), s.effects
}

func awaitRedeliveryGate(t *testing.T, ctx context.Context, state *redeliveryState, expected int) {
	t.Helper()
	select {
	case attempt := <-state.entered:
		if attempt != expected {
			t.Fatalf("gate attempt = %d, want %d", attempt, expected)
		}
	case <-ctx.Done():
		t.Fatal("command did not reach delivery gate", ctx.Err())
	}
}

// The kernel exposes these states through snapshots, not a test notification.
// Poll only the read: mutations (append and explicit recovery) are never retried.
func awaitRedeliveryState(t *testing.T, ctx context.Context, description string, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(description, ctx.Err())
		case <-ticker.C:
		}
	}
}
