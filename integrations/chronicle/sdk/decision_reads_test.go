// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"errors"
	"testing"

	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/compliance"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/readmodels"
	"github.com/cratis/chronicle.go/transactions"
	"google.golang.org/grpc"
)

type decisionModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type decisionNoRPC struct{ calls int }

func (c *decisionNoRPC) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	c.calls++
	return errors.New("unexpected decision RPC")
}
func (c *decisionNoRPC) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	c.calls++
	return nil, errors.New("unexpected decision stream")
}

func decisionParticipantForTest(t *testing.T, connection *decisionNoRPC) (*participant, *transactions.Owner) {
	t.Helper()
	event, err := events.Define[event]()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := events.NewCatalog(event.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := eventsequences.New("store", "tenant", events.EventLog, catalog, connection)
	if err != nil {
		t.Fatal(err)
	}
	unit, owner, err := transactions.Begin(t.Context(), sequence)
	if err != nil {
		t.Fatal(err)
	}
	return &participant{unit: unit, coordinates: integration.Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}}, owner
}

func decisionProviderForTest(t *testing.T, connection *decisionNoRPC, options ...readmodels.ModelOption) decisionProvider[decisionModel] {
	t.Helper()
	options = append([]readmodels.ModelOption{readmodels.WithIdentifier("decision-model"), readmodels.WithObserver(readmodels.Projection, "decision-projection")}, options...)
	model, err := readmodels.Define[decisionModel](options...)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := readmodels.NewCatalog(model.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	service, err := readmodels.New("store", "tenant", catalog, connection)
	if err != nil {
		t.Fatal(err)
	}
	return newDecisionProvider(service, model)
}

func TestDecisionSDKRefusesClassifiedAndUnsupportedProvidersWithoutRPC(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option readmodels.ModelOption
		reason readmodels.DecisionReadRefusalReason
	}{
		{name: "low-level-provider", reason: readmodels.DecisionUnavailable},
		{name: "pii", option: readmodels.WithProtection(compliance.Property("name", compliance.Classification{PII: true})), reason: readmodels.DecisionProtectedModel},
		{name: "subject", option: readmodels.WithProtection(compliance.Property("name", compliance.Classification{Encrypted: true})), reason: readmodels.DecisionProtectedModel},
		{name: "namespace", option: readmodels.WithProtection(compliance.Property("name", compliance.Classification{Encrypted: true, Scope: compliance.Namespace})), reason: readmodels.DecisionProtectedModel},
		{name: "global", option: readmodels.WithProtection(compliance.Property("name", compliance.Classification{Encrypted: true, Scope: compliance.Global})), reason: readmodels.DecisionProtectedModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection := &decisionNoRPC{}
			var options []readmodels.ModelOption
			if tc.option != nil {
				options = append(options, tc.option)
			}
			provider := decisionProviderForTest(t, connection, options...)
			participant, owner := decisionParticipantForTest(t, connection)
			for _, detached := range []bool{false, true} {
				var read *decisionRead[decisionModel]
				var err error
				if detached {
					read, err = provider.detached(t.Context(), "source")
				} else {
					read, err = provider.acquire(t.Context(), "source", participant)
				}
				var refused *readmodels.DecisionReadRefused
				if read != nil || !errors.As(err, &refused) || refused.Reason != tc.reason {
					t.Fatalf("read = %v, error = %v; want %s", read, err, tc.reason)
				}
			}
			if err := owner.Rollback(); err != nil {
				t.Fatal(err)
			}
			if connection.calls != 0 || len(participant.unit.GetEvents()) != 0 {
				t.Fatal("refusal acquired, appended or released", connection.calls)
			}
		})
	}
}

func TestDecisionSDKRequiresOpenParticipantBeforeAcquisition(t *testing.T) {
	for _, kind := range []string{"nil", "zero", "wrong-sequence", "completed"} {
		t.Run(kind, func(t *testing.T) {
			connection := &decisionNoRPC{}
			provider := decisionProviderForTest(t, connection)
			p, owner := decisionParticipantForTest(t, connection)
			want := integration.ErrUnsupported
			switch kind {
			case "nil":
				p = nil
			case "zero":
				p.unit = &transactions.UnitOfWork{}
			case "wrong-sequence":
				p.coordinates.Sequence = "other"
			case "completed":
				want = transactions.ErrCompleted
			}
			if err := owner.Rollback(); err != nil {
				t.Fatal(err)
			}
			read, err := provider.acquire(t.Context(), "source", p)
			if read != nil || !errors.Is(err, want) || connection.calls != 0 {
				t.Fatalf("read = %v, err = %v, RPCs = %d", read, err, connection.calls)
			}
		})
	}
}

func TestDecisionSDKProgressAndModelPresenceCannotManufactureEvidence(t *testing.T) {
	connection := &decisionNoRPC{}
	participant, owner := decisionParticipantForTest(t, connection)
	position := events.SequenceNumber(42)
	// This is deliberately NOT a token fixture. LastHandled and a keyed model
	// cannot fill the SDK's private issuance state, even when the instance exists.
	for _, read := range []*decisionRead[decisionModel]{nil, {}, {instance: readmodels.Instance[decisionModel]{Exists: true, LastHandled: &position}}} {
		if err := enrollDecision(t.Context(), participant, read); !errors.Is(err, transactions.ErrInvalidDecision) {
			t.Fatalf("unissued read enrolled: %v", err)
		}
	}
	if connection.calls != 0 || len(participant.unit.GetEvents()) != 0 {
		t.Fatal("invalid enrollment performed work")
	}
	if err := owner.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionSDKCancellationDoesNotAcquireOrEnroll(t *testing.T) {
	connection := &decisionNoRPC{}
	provider := decisionProviderForTest(t, connection)
	participant, owner := decisionParticipantForTest(t, connection)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := provider.acquire(ctx, "source", participant); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(read, err)
	}
	if err := enrollDecision(ctx, participant, &decisionRead[decisionModel]{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if read, err := provider.detached(nil, "source"); read != nil || !errors.Is(err, integration.ErrInvalid) { //nolint:staticcheck // Deliberately exercise invalid context admission (SA1012).
		t.Fatal(read, err)
	}
	if err := enrollDecision[decisionModel](nil, participant, nil); !errors.Is(err, integration.ErrInvalid) { //nolint:staticcheck // Deliberately exercise invalid context admission (SA1012).
		t.Fatal(err)
	}
	if err := owner.Rollback(); err != nil {
		t.Fatal(err)
	}
	if connection.calls != 0 {
		t.Fatal("cancellation used RPC")
	}
}
