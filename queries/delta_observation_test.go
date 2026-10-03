// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

type DeltaItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Private string `json:"private"`
}

func deltaRegistry(t *testing.T) *queries.Registry {
	t.Helper()
	var r queries.Registry
	source := observable.FromProducer(func(_ context.Context, emit func(queries.ObservedCollection[DeltaItem]) error) error {
		for i, items := range [][]DeltaItem{
			{{ID: "a", Name: "first", Private: "secret"}},
			{{ID: "b", Name: "suppressed", Private: "secret"}},
			{{ID: "c", Name: "last", Private: "secret"}},
		} {
			if err := emit(queries.ObservedCollection[DeltaItem]{Items: items, Version: uint64(i + 1), PreviousVersion: uint64(i), Generation: "g", Changes: []queries.CollectionChange{}}); err != nil {
				return err
			}
		}
		return nil
	})
	mustRegister(t, queries.RegisterObservable[DeltaItem](&r, "All", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[queries.ObservedCollection[DeltaItem]], error) {
		return source, nil
	}), public[queries.NoArguments]()))
	mustRegister(t, queries.RegisterReadModelInterceptor(&r, "mask", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[DeltaItem], error) {
		return queries.InterceptorFunc[DeltaItem](func(_ context.Context, item DeltaItem) (DeltaItem, error) { item.Private = "masked"; return item, nil }), nil
	}))
	calls := 0
	mustRegister(t, r.AddEmissionGuard("skip", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
			calls++
			if c.FirstDelivered() != (calls == 1) {
				t.Errorf("first-delivered flag call %d", calls)
			}
			if calls == 2 {
				return queries.Suppress, nil
			}
			return queries.Allow, nil
		}), nil
	}))
	return &r
}
func TestDeltaObservationSuppressionAndKnownHintsUseDeliveredMaskedBaseline(t *testing.T) {
	p := observationPipeline(t, deltaRegistry(t), queries.PipelineOptions{})
	o, _, err := p.Open(context.Background(), "DeltaItem.All", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	retained := int64(0)
	var frames []string
	err = o.Run(context.Background(), queries.ObservationOptions{TransferMode: queries.Delta, ReserveBaseline: func(n int64) (func(), error) { retained += n; return func() { retained -= n }, nil }}, func(result queries.Result[any]) error {
		body, err := result.MarshalJSON()
		if err != nil {
			return err
		}
		frames = append(frames, string(body))
		return nil
	})
	if err != nil || len(frames) != 2 || retained != 0 {
		t.Fatalf("run = %v, frames %d, bytes %d", err, len(frames), retained)
	}
	if strings.Contains(frames[0], `"changeSet"`) || !strings.Contains(frames[0], `"id":"a"`) {
		t.Fatal(frames[0])
	}
	if strings.Contains(frames[1], `"data":`) || !strings.Contains(frames[1], `"added":[{"id":"c","name":"last","private":"masked"}]`) || !strings.Contains(frames[1], `"removed":[{"id":"a","name":"first","private":"masked"}]`) || strings.Contains(frames[1], "secret") || strings.Contains(frames[1], "suppressed") {
		t.Fatal(frames[1])
	}
	mustRegister(t, o.Close(context.Background()))
}
func TestDeltaObservationFailedDeliveryReleasesBaselineAndStopsSource(t *testing.T) {
	p := observationPipeline(t, deltaRegistry(t), queries.PipelineOptions{})
	o, _, err := p.Open(context.Background(), "DeltaItem.All", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	retained := int64(0)
	calls := 0
	failure := errors.New("write failed")
	err = o.Run(context.Background(), queries.ObservationOptions{TransferMode: queries.Delta, ReserveBaseline: func(n int64) (func(), error) { retained += n; return func() { retained -= n }, nil }}, func(queries.Result[any]) error { calls++; return failure })
	if !errors.Is(err, failure) || calls != 1 || retained != 0 {
		t.Fatalf("failure = %v, calls %d, bytes %d", err, calls, retained)
	}
	mustRegister(t, o.Close(context.Background()))
}

type IdentitylessItem struct {
	Name string `json:"name"`
}

func TestIdentitylessDeltaObservationKeepsFullSnapshots(t *testing.T) {
	source := observable.FromProducer(func(_ context.Context, emit func([]IdentitylessItem) error) error {
		if err := emit([]IdentitylessItem{{Name: "first"}}); err != nil {
			return err
		}
		return emit([]IdentitylessItem{{Name: "next"}})
	})
	var r queries.Registry
	mustRegister(t, queries.RegisterObservable[IdentitylessItem](&r, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[[]IdentitylessItem], error) {
		return source, nil
	}), public[queries.NoArguments]()))
	p := observationPipeline(t, &r, queries.PipelineOptions{})
	o, _, err := p.Open(context.Background(), "IdentitylessItem.Observe", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = o.Run(context.Background(), queries.ObservationOptions{TransferMode: queries.Delta, ReserveBaseline: func(int64) (func(), error) { t.Error("identity-less baseline reserved"); return func() {}, nil }}, func(result queries.Result[any]) error {
		calls++
		if _, present := result.Data(); !present || result.Details().ChangeSet != nil {
			t.Error("identity-less delta lost full snapshot")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("run %v, frames %d", err, calls)
	}
	mustRegister(t, o.Close(context.Background()))
}
