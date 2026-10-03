// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"errors"
	"testing"

	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/transactions"
)

type originTransport struct {
	transport
	resolver eventsequences.AppendOriginResolver
}

func (t *originTransport) AppendOriginResolver() eventsequences.AppendOriginResolver {
	return t.resolver
}

func TestUnitCommitBypassesResolverAndCannotCountAsImmediateAppend(t *testing.T) {
	definition, err := events.Define[event]()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := events.NewCatalog(definition.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	resolved := 0
	connection := &originTransport{resolver: func(context.Context) (eventsequences.Origin, bool, error) {
		resolved++
		return eventsequences.Origin{}, true, errors.New("private resolver failure")
	}}
	sequence, err := eventsequences.New("store", "tenant", "event-log", catalog, connection)
	if err != nil {
		t.Fatal(err)
	}
	immediate := eventsequences.NewOrigin()
	ctx := eventsequences.WithOrigin(t.Context(), immediate)
	var observed eventsequences.Origin
	stopAll := sequence.OnAppend(func(n eventsequences.AppendNotification) { observed = n.Origin })
	defer stopAll()
	calls := 0
	stopImmediate := subscribeAppends(sequence.OnAppend, immediate, func(integration.CommitResult, error) { calls++ })
	defer stopImmediate()
	unit, owner, err := transactions.Begin(auditContext(ctx), sequence)
	if err != nil {
		t.Fatal(err)
	}
	participant := participant{unit: unit, coordinates: integration.Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}}
	if err := participant.Stage(ctx, integration.Batch{Entries: []integration.Entry{{Source: "source", Event: event{Name: "event"}}}, Scopes: []integration.LabeledScope{{Label: "source", Expectation: integration.Expectation{Kind: integration.NoCheck}}}}); err != nil {
		t.Fatal(err)
	}
	result, err := owner.Commit(ctx)
	if err != nil || result.Err() != nil || connection.calls != 1 || resolved != 0 || calls != 0 || observed == immediate || observed == (eventsequences.Origin{}) || observed != unit.Origin() {
		t.Fatal(result, err, connection.calls, resolved, calls, observed)
	}
}

func TestResolverErrorAndPanicFailBeforeDispatchWithoutLeakingPayload(t *testing.T) {
	for _, panicked := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicked], func(t *testing.T) {
			definition, err := events.Define[event]()
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := events.NewCatalog(definition.Descriptor())
			if err != nil {
				t.Fatal(err)
			}
			secret := errors.New("private payload")
			connection := &originTransport{resolver: func(context.Context) (eventsequences.Origin, bool, error) {
				if panicked {
					panic(secret)
				}
				return eventsequences.NewOrigin(), false, secret
			}}
			sequence, err := eventsequences.New("store", "tenant", "event-log", catalog, connection)
			if err != nil {
				t.Fatal(err)
			}
			notified := false
			stop := sequence.OnAppend(func(eventsequences.AppendNotification) { notified = true })
			defer stop()
			result, err := sequence.AppendBatch(eventsequences.WithOrigin(t.Context(), eventsequences.NewOrigin()), []eventsequences.Entry{{Source: "source", Event: event{Name: "event"}}})
			var failure *eventsequences.AppendOriginResolutionError
			if !errors.As(err, &failure) || failure.Panicked != panicked || errors.Is(err, secret) || connection.calls != 0 || notified || result.Disposition != eventsequences.Rejected {
				t.Fatal(result, err, connection.calls, notified)
			}
		})
	}
}
