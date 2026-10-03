// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/contracts/sequences"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/transactions"
	"google.golang.org/grpc"
)

type event struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}
type transport struct {
	request *sequences.AppendManyForEventSourcesRequest
	calls   int
	failure error
}

func (t *transport) Invoke(_ context.Context, _ string, input, output any, _ ...grpc.CallOption) error {
	request, ok := input.(*sequences.AppendManyForEventSourcesRequest)
	if !ok {
		return errors.New("unexpected RPC")
	}
	t.calls++
	t.request = request
	if t.failure != nil {
		return t.failure
	}
	positions := make([]uint64, len(request.Events))
	for index := range positions {
		positions[index] = uint64(index)
	}
	response := output.(*sequences.CommandResult_AppendManyResponse)
	response.IsAuthorized = true
	response.Response = &sequences.AppendManyResponse{IsSuccess: true, SequenceNumbers: positions, CorrelationId: request.CorrelationId}
	return nil
}
func (t *transport) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("unexpected stream")
}
func TestSDKStagesSnapshotsAndDispatchesOneOrderedAtomicBatch(t *testing.T) {
	for _, lost := range []bool{false, true} {
		definition, err := events.Define[event]()
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := events.NewCatalog(definition.Descriptor())
		if err != nil {
			t.Fatal(err)
		}
		connection := &transport{}
		cause := errors.New("lost acknowledgement")
		if lost {
			connection.failure = cause
		}
		sequence, err := eventsequences.New("store", "tenant", "event-log", catalog, connection)
		if err != nil {
			t.Fatal(err)
		}
		id, err := concepts.ParseUUID("00112233-4455-6677-8899-aabbccddeeff")
		if err != nil {
			t.Fatal(err)
		}
		ctx := correlation.WithID(t.Context(), id)
		ctx = integration.WithMetadata(ctx, integration.Metadata{Actor: integration.Actor{Subject: "verified", Name: "Name"}})
		unit, owner, err := transactions.Begin(auditContext(ctx), sequence)
		if err != nil {
			t.Fatal(err)
		}
		p := participant{unit: unit, coordinates: integration.Coordinates{Store: "store", Namespace: "tenant", Sequence: "event-log"}}
		values := []string{"snapshot"}
		batch := integration.Batch{Entries: []integration.Entry{{Source: "A", Event: event{Name: "A1", Values: values}}, {Source: "B", Event: event{Name: "B1"}}, {Source: "A", Event: event{Name: "A2"}}}, Scopes: []integration.LabeledScope{{Label: "A", Expectation: integration.Expectation{Kind: integration.NoCheck}}, {Label: "B", Expectation: integration.Expectation{Kind: integration.NoCheck}}}}
		if err := p.Stage(ctx, batch); err != nil {
			t.Fatal(err)
		}
		values[0] = "changed"
		completion := completion{owner: owner, unit: unit}
		result, err := completion.Commit(ctx)
		if connection.calls != 1 || len(connection.request.Events) != 3 || connection.request.Events[0].Content != "{\"name\":\"A1\",\"values\":[\"snapshot\"]}" {
			t.Fatal(connection.request, err)
		}
		if connection.request.CausedBy.Subject != "verified" || connection.request.CorrelationId.Lo != 0x6677445500112233 || connection.request.CorrelationId.Hi != 0xffeeddccbbaa9988 {
			t.Fatal(connection.request.CorrelationId, connection.request.CausedBy)
		}
		if lost {
			if result.Report.Disposition != commands.OutcomeUnknown || !errors.Is(err, cause) {
				t.Fatal(result, err)
			}
		} else if err != nil || result.Report.Disposition != commands.Committed {
			t.Fatal(result, err)
		}
		_, _ = completion.Commit(ctx)
		if connection.calls != 1 {
			t.Fatal("retried commit")
		}
	}
}
func TestOpaqueResolvedScopeCannotCrossCoordinates(t *testing.T) {
	a := integration.Coordinates{Store: "store", Namespace: "A", Sequence: "event-log"}
	token := resolvedScope{coordinates: a, scope: eventsequences.Scope{Expectation: eventsequences.Exact(0)}}
	scope := integration.LabeledScope{Label: "id", Expectation: integration.Expectation{Kind: integration.ProviderResolved, Token: token}}
	value, err := toScope(scope, a)
	if err != nil || !reflect.DeepEqual(value, token.scope) {
		t.Fatal(value, err)
	}
	a.Namespace = "B"
	if _, err := toScope(scope, a); !errors.Is(err, integration.ErrMismatch) {
		t.Fatal(err)
	}
}
