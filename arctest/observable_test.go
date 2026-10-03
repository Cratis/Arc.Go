// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arctest_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/arctest"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func TestObservableScenarioCapturesThroughInterceptionGuardsAndJoinsProducer(t *testing.T) {
	joined := make(chan struct{})
	source := observable.FromProducer(func(_ context.Context, emit func(person) error) error {
		defer close(joined)
		for _, name := range []string{"first", "suppressed", "last", "not captured"} {
			if err := emit(person{Name: name}); err != nil {
				return err
			}
		}
		return nil
	})
	builder := builderFor(t, arc.Options{})
	must(t, queries.RegisterObservable[person](builder, "Watch", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[person], error) { return source, nil })))
	must(t, queries.RegisterReadModelInterceptor(builder.Queries(), "mask", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[person], error) {
		return queries.InterceptorFunc[person](func(_ context.Context, p person) (person, error) { p.Name += "!"; return p, nil }), nil
	}))
	calls := 0
	must(t, builder.Queries().AddEmissionGuard("skip", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(context.Context, queries.EmissionContext) (queries.EmissionVerdict, error) {
			calls++
			if calls == 2 {
				return queries.Suppress, nil
			}
			return queries.Allow, nil
		}), nil
	}))
	scenario := arctest.NewObservableQuery[person](arctest.New(t, builder), "person.Watch")
	results, err := scenario.Capture(t.Context(), queries.Request{}, arctest.CaptureOptions{MaxResults: 2})
	if err != nil || len(results) != 2 {
		t.Fatalf("capture = %d, %v", len(results), err)
	}
	var names []string
	for _, result := range results {
		value, present := result.Data()
		if !present || !result.IsSuccess() {
			t.Fatal(result)
		}
		names = append(names, value.Name)
	}
	if !reflect.DeepEqual(names, []string{"first!", "last!"}) {
		t.Fatal(names)
	}
	select {
	case <-joined:
	default:
		t.Fatal("successful capture returned before producer joined")
	}
}
func TestObservableScenarioByteCapacityCancellationAndNoActivationOnTypeMismatch(t *testing.T) {
	builder := builderFor(t, arc.Options{})
	state, err := observable.NewState(person{Name: "first"}, observable.SubjectOptions[person]{})
	must(t, err)
	calls := 0
	must(t, queries.RegisterObservable[person](builder, "Watch", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[person], error) {
		calls++
		return state, nil
	})))
	fixture := arctest.New(t, builder)
	wrong := arctest.NewObservableQuery[[]person](fixture, "person.Watch")
	if _, err := wrong.Capture(t.Context(), queries.Request{}, arctest.CaptureOptions{}); !errors.Is(err, queries.ErrResponseType) || calls != 0 {
		t.Fatalf("type mismatch = %v, activations %d", err, calls)
	}
	scenario := arctest.NewObservableQuery[person](fixture, "person.Watch")
	results, err := scenario.Capture(t.Context(), queries.Request{}, arctest.CaptureOptions{MaxBytes: 1})
	if !errors.Is(err, arctest.ErrCaptureCapacity) || len(results) != 0 {
		t.Fatalf("capacity = %d, %v", len(results), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scenario.Capture(ctx, queries.Request{}, arctest.CaptureOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("canceled source activated: %d", calls)
	}
}
func TestObservableScenarioPendingTimeoutJoinsBeforeReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		builder := builderFor(t, arc.Options{})
		state, err := observable.NewPendingState(observable.SubjectOptions[person]{})
		must(t, err)
		must(t, queries.RegisterObservable[person](builder, "Watch", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[person], error) { return state, nil })))
		fixture, err := arctest.NewScenario(t.Context(), builder)
		must(t, err)
		results, err := arctest.NewObservableQuery[person](fixture, "person.Watch").Capture(t.Context(), queries.Request{}, arctest.CaptureOptions{Timeout: time.Second})
		if !errors.Is(err, context.DeadlineExceeded) || len(results) != 0 {
			t.Fatalf("pending timeout = %d, %v", len(results), err)
		}
		must(t, fixture.Close(t.Context()))
	})
}

type observableTestSource struct{ cleanup error }

func (s observableTestSource) Open(context.Context) (observable.Stream[person], error) { return s, nil }
func (s observableTestSource) Next(context.Context) (person, error) {
	return person{Name: "first"}, nil
}
func (s observableTestSource) Close(context.Context) error { return s.cleanup }
func TestObservableScenarioCountLimitNeverHidesCleanupFailure(t *testing.T) {
	failure := errors.New("cleanup failed")
	builder := builderFor(t, arc.Options{})
	must(t, queries.RegisterObservable[person](builder, "Watch", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[person], error) {
		return observableTestSource{failure}, nil
	})))
	fixture := arctest.New(t, builder)
	results, err := arctest.NewObservableQuery[person](fixture, "person.Watch").Capture(t.Context(), queries.Request{}, arctest.CaptureOptions{MaxResults: 1})
	if !errors.Is(err, failure) || len(results) != 1 {
		t.Fatalf("count limit masked cleanup = %d, %v", len(results), err)
	}
}
func ExampleNewObservableQuery() {
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		panic(err)
	}
	state, err := observable.NewState(person{Name: "Ada"}, observable.SubjectOptions[person]{})
	if err != nil {
		panic(err)
	}
	err = queries.RegisterObservable[person](builder, "Watch", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[person], error) { return state, nil }))
	if err != nil {
		panic(err)
	}
	fixture, err := arctest.NewScenario(context.Background(), builder)
	if err != nil {
		panic(err)
	}
	results, err := arctest.NewObservableQuery[person](fixture, "person.Watch").Capture(context.Background(), queries.Request{}, arctest.CaptureOptions{MaxResults: 1})
	if err != nil {
		panic(err)
	}
	value, _ := results[0].Data()
	fmt.Println(value.Name)
	if err := fixture.Close(context.Background()); err != nil {
		panic(err)
	}
	// Output: Ada
}
