// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/eventsequences"
)

func namedHistory(t *testing.T, a *adapter) eventsequences.History {
	t.Helper()
	source := events.SourceID("source")
	history := eventsequences.History{Filter: eventsequences.ScopeFilter{SourceID: &source}}
	for index, value := range []any{previousNamedEvent{LegacyName: "previous", ExternalID: "old-external"}, namedEvent{Name: "current", ExternalID: "external"}} {
		d, ok := a.events.Lookup(value)
		if !ok {
			t.Fatal("fixture descriptor missing")
		}
		body, err := d.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		history.Events = append(history.Events, events.Appended{Context: events.Context{EventType: d.Ref(), SequenceNumber: events.SequenceNumber(index)}, Content: body,
			// The exact persisted generation must win, never an alternate/current guess.
			GenerationalContent: map[events.Generation]json.RawMessage{1: json.RawMessage(`{"LEGACYNAME":"wrong"}`), 2: json.RawMessage(`{"NAME":"wrong"}`)}})
	}
	return history
}

func TestHistoryUsesExactGenerationSDKCodec(t *testing.T) {
	a := decoderAdapter(t)
	history := namedHistory(t, a)
	request := integration.HistoryRequest{Filter: integration.Filter{Source: "source"}}
	got, err := a.decodeHistory(request, history)
	want := []integration.RecordedEvent{
		{Event: previousNamedEvent{LegacyName: "previous", ExternalID: "old-external"}, Type: integration.EventType{ID: "named", Generation: 1}, Position: 0},
		{Event: namedEvent{Name: "current", ExternalID: "external"}, Type: integration.EventType{ID: "named", Generation: 2}, Position: 1},
	}
	if err != nil || !reflect.DeepEqual(got.Events, want) || got.Scope.Label != "source" || got.Scope.Filter.Source != "source" || got.Scope.Expectation != (integration.Expectation{Kind: integration.UpperBound, Position: 1}) {
		t.Fatalf("history = %+v, %v; want %+v", got, err, want)
	}
	for _, indexes := range [][]int{{0}, {1}, {0, 1}} {
		raw := eventsequences.History{Filter: history.Filter}
		var expected []integration.RecordedEvent
		for _, index := range indexes {
			event := history.Events[index]
			event.GenerationalContent = nil
			raw.Events = append(raw.Events, event)
			expected = append(expected, want[index])
		}
		got, err := a.decodeHistory(request, raw)
		if err != nil || !reflect.DeepEqual(got.Events, expected) || got.Scope.Expectation.Position != expected[len(expected)-1].Position {
			t.Fatalf("raw generations %v = %+v, %v; want %+v", indexes, got, err, expected)
		}
	}
	got, err = a.decodeHistory(request, eventsequences.History{Filter: history.Filter})
	if err != nil || len(got.Events) != 0 || got.Scope.Expectation.Kind != integration.NoMatchingEvent {
		t.Fatalf("empty history = %+v, %v", got, err)
	}
}

func TestHistoryRefusesMalformedUnknownAndMismatchedGenerationsWithoutPartialHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  events.TypeRef
		body string
		want error
	}{
		{"malformed", events.TypeRef{ID: "named", Generation: 2}, `{"name":"decoded-first","NAME":42}`, chronicle.ErrProtocol},
		{"not an object", events.TypeRef{ID: "named", Generation: 2}, "null", chronicle.ErrProtocol},
		{"unknown ID", events.TypeRef{ID: "unknown", Generation: 2}, `{}`, integration.ErrUnsupported},
		{"unknown generation", events.TypeRef{ID: "named", Generation: 3}, `{}`, integration.ErrUnsupported},
		{"zero generation", events.TypeRef{ID: "named"}, `{}`, integration.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := decoderAdapter(t)
			history := namedHistory(t, a)
			history.Events[1].Context.EventType = tc.ref
			history.Events[1].Content = []byte(tc.body)
			got, err := a.decodeHistory(integration.HistoryRequest{Filter: integration.Filter{Source: "source"}}, history)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, integration.History{}) {
				t.Fatalf("failed history = %+v, %v; want empty, %v", got, err, tc.want)
			}
		})
	}
}

func TestHistoryRefusesUnregisteredHistoricalGenerationDespiteCurrentAlternate(t *testing.T) {
	a := decoderAdapter(t)
	history := namedHistory(t, a)
	current, ok := a.events.Lookup(namedEvent{})
	if !ok {
		t.Fatal("fixture current descriptor missing")
	}
	catalog, err := events.NewCatalog(current)
	if err != nil {
		t.Fatal(err)
	}
	a.events = catalog
	got, err := a.decodeHistory(integration.HistoryRequest{Filter: integration.Filter{Source: "source"}}, history)
	if !errors.Is(err, integration.ErrUnsupported) || !reflect.DeepEqual(got, integration.History{}) {
		t.Fatalf("unregistered history = %+v, %v", got, err)
	}
}

type decoderHistoryReader struct {
	a       *adapter
	history eventsequences.History
}

func (r decoderHistoryReader) ReadHistory(_ context.Context, request integration.HistoryRequest) (integration.History, error) {
	return r.a.decodeHistory(request, r.history)
}

type decoderTransaction struct{ commits, stages, rollbacks int }

func (p *decoderTransaction) Begin(context.Context, integration.Coordinates) (integration.Participant, integration.CompletionOwner, error) {
	return p, p, nil
}
func (p *decoderTransaction) Stage(context.Context, integration.Batch) error { p.stages++; return nil }
func (p *decoderTransaction) Commit(context.Context) (integration.CommitResult, error) {
	p.commits++
	return integration.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}, nil
}
func (p *decoderTransaction) Rollback() error { p.rollbacks++; return nil }

type decoderAggregate struct {
	*integration.AggregateRoot
	IDs []string
}
type decoderCommand struct{ ID integration.EventSourceID }

func TestAggregateFoldsSDKDecodedHistoryAndNeverFoldsFailedHistory(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete history", true: "failed later decode"}[invalid], func(t *testing.T) {
			a := decoderAdapter(t)
			history := namedHistory(t, a)
			if invalid {
				history.Events[1].Content = []byte(`{"NAME":42}`)
			}
			transaction := &decoderTransaction{}
			builder, err := arc.NewBuilder(arc.Options{})
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := integration.New(integration.Options{Events: a, Transactions: transaction, History: decoderHistoryReader{a: a, history: history}, StoreResolver: func(context.Context, commands.CommandContext) (integration.Coordinates, error) {
				return integration.Coordinates{Store: "store", Namespace: "Default", Sequence: "event-log"}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := bridge.Install(builder); err != nil {
				t.Fatal(err)
			}
			folds := 0
			factory, err := integration.DefineAggregate(func(root *integration.AggregateRoot) *decoderAggregate { return &decoderAggregate{AggregateRoot: root} },
				integration.OnAggregateEvent(func(a *decoderAggregate, e previousNamedEvent) error {
					folds++
					a.IDs = append(a.IDs, e.LegacyName, e.ExternalID)
					return nil
				}),
				integration.OnAggregateEvent(func(a *decoderAggregate, e namedEvent) error {
					folds++
					a.IDs = append(a.IDs, e.Name, e.ExternalID)
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			if err := commands.Register[decoderCommand](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ decoderCommand) (commands.Outcome[[]string], error) {
				aggregate, err := factory.Get(ctx, inv)
				if err != nil {
					return commands.Outcome[[]string]{}, err
				}
				return commands.Respond(aggregate.IDs), nil
			})); err != nil {
				t.Fatal(err)
			}
			app, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			if err := app.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := app.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			result, err := app.Commands().Execute(t.Context(), decoderCommand{ID: "source"})
			if invalid {
				if !errors.Is(err, chronicle.ErrProtocol) || result.IsSuccess() || folds != 0 || transaction.commits != 0 || transaction.stages != 0 || transaction.rollbacks != 0 {
					t.Fatalf("invalid fold = %+v, %v; folds=%d transaction=%+v", result, err, folds, transaction)
				}
				return
			}
			response, ok := result.Response()
			if err != nil || !result.IsSuccess() || !ok || !reflect.DeepEqual(response, []string{"previous", "old-external", "current", "external"}) || folds != 2 || transaction.commits != 1 {
				t.Fatalf("fold = %+v, %v; response=%+v folds=%d transaction=%+v", result, err, response, folds, transaction)
			}
		})
	}
}
