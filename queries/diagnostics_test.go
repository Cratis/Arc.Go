// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/queries"
)

func diagnosticRecorder(t *testing.T) *observability.Recorder {
	t.Helper()
	r, err := observability.NewRecorder(observability.Options{})
	mustRegister(t, err)
	return r
}

func TestDiagnosticsCountTypedScopedUnknownBindingAndCancelledQueriesOnce(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var registry queries.Registry
	calls, clocks, opens := 0, 0, 0
	mustRegister(t, queries.Register[Item](&registry, "Current", queries.Function(func(context.Context, queries.NoArguments) (Item, error) { calls++; return Item{}, nil }), public[queries.NoArguments]()))
	p := build(t, &registry, queries.PipelineOptions{Diagnostics: recorder, Clock: func() time.Time { clocks++; return time.Unix(1, 0) }, OpenResources: func(context.Context) (execution.Resources, error) { opens++; return &plainResources{}, nil }})
	if calls != 0 || clocks != 0 || opens != 0 {
		t.Fatal("activated at build")
	}
	_, err := queries.Perform[string](t.Context(), p, "Item.Current", queries.Request{})
	if !errors.Is(err, queries.ErrResponseType) {
		t.Fatal(err)
	}
	_, err = p.PerformScoped(t.Context(), nil, "Item.Current", queries.Request{})
	if !errors.Is(err, execution.ErrInvalidScope) {
		t.Fatal(err)
	}
	_, err = p.Perform(t.Context(), "SECRET unknown", queries.Request{})
	if !errors.Is(err, queries.ErrUnknownQuery) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = queries.Perform[Item](ctx, p, "Item.Current", queries.Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = queries.Perform[Item](t.Context(), p, "Item.Current", queries.Request{})
	mustRegister(t, err)
	s := recorder.Snapshot()
	want := []observability.Outcome{observability.Error, observability.Error, observability.Error, observability.Cancelled, observability.Success}
	if calls != 1 || opens != 1 || clocks != 2 || len(s.Events) != len(want) {
		t.Fatal(calls, opens, clocks, s)
	}
	for i, outcome := range want {
		if s.Events[i].Outcome != outcome || s.Events[i].Transport != observability.SnapshotTransport {
			t.Fatal(i, s.Events[i])
		}
	}
	if s.Events[2].Artifact != observability.Other {
		t.Fatal(s.Events[2])
	}
}

func TestDiagnosticsBindingFailureUsesValidationOutcomeBeforeResources(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var registry queries.Registry
	mustRegister(t, queries.Register[Item](&registry, "Boolean", queries.Function(func(context.Context, boolArguments) (Item, error) { t.Fatal("invoked"); return Item{}, nil })))
	p := build(t, &registry, queries.PipelineOptions{Diagnostics: recorder, OpenResources: func(context.Context) (execution.Resources, error) { t.Fatal("opened resources"); return nil, nil }})
	arguments, err := queries.NewArguments(map[string]any{"flag": "SECRET invalid boolean"})
	mustRegister(t, err)
	result, err := p.Perform(t.Context(), "Item.Boolean", queries.NewRequest(arguments, queries.Parameters{}))
	if err == nil || result.IsValid() || result.HasExceptions() {
		t.Fatal(result, err)
	}
	s := recorder.Snapshot()
	if len(s.Events) != 1 || s.Events[0].Outcome != observability.Validation {
		t.Fatal(s)
	}
	body, err := json.Marshal(s)
	mustRegister(t, err)
	if strings.Contains(string(body), "SECRET") {
		t.Fatal(string(body))
	}
}

func TestDiagnosticsUnknownQueryDoesNotBorrowAnotherPipelinesLabel(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var first, second queries.Registry
	mustRegister(t, queries.Register[Item](&first, "First", itemPerformer(), public[queries.NoArguments]()))
	mustRegister(t, queries.Register[Item](&second, "Second", itemPerformer(), public[queries.NoArguments]()))
	p := build(t, &first, queries.PipelineOptions{Diagnostics: recorder})
	_ = build(t, &second, queries.PipelineOptions{Diagnostics: recorder})
	_, err := p.Perform(t.Context(), "Item.Second", queries.Request{})
	if !errors.Is(err, queries.ErrUnknownQuery) || recorder.Snapshot().Events[0].Artifact != observability.Other {
		t.Fatal(err, recorder.Snapshot())
	}
}

func TestDiagnosticsQueryFactoryPanicIsAnErrorWithoutPayload(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var registry queries.Registry
	mustRegister(t, queries.Register[Item](&registry, "Current", itemPerformer(), public[queries.NoArguments]()))
	p := build(t, &registry, queries.PipelineOptions{Diagnostics: recorder, OpenResources: func(context.Context) (execution.Resources, error) { panic("SECRET resource panic") }})
	result, err := queries.Perform[Item](t.Context(), p, "Item.Current", queries.Request{})
	var panicErr *execution.PanicError
	if !errors.As(err, &panicErr) || !result.HasExceptions() {
		t.Fatal(result, err)
	}
	s := recorder.Snapshot()
	if len(s.Events) != 1 || s.Events[0].Outcome != observability.Error {
		t.Fatal(s)
	}
	body, err := json.Marshal(s)
	mustRegister(t, err)
	if strings.Contains(string(body), "SECRET") {
		t.Fatal(string(body))
	}
}

func TestDiagnosticsNestedQueryFromResourceFactoryIsIndependent(t *testing.T) {
	recorder := diagnosticRecorder(t)
	var outer, inner queries.Registry
	mustRegister(t, queries.Register[Item](&inner, "Inner", itemPerformer(), public[queries.NoArguments]()))
	inside := build(t, &inner, queries.PipelineOptions{Diagnostics: recorder})
	mustRegister(t, queries.Register[Item](&outer, "Outer", itemPerformer(), public[queries.NoArguments]()))
	outside := build(t, &outer, queries.PipelineOptions{Diagnostics: recorder, OpenResources: func(ctx context.Context) (execution.Resources, error) {
		_, err := queries.Perform[Item](ctx, inside, "Item.Inner", queries.Request{})
		return &plainResources{}, err
	}})
	_, err := queries.Perform[Item](t.Context(), outside, "Item.Outer", queries.Request{})
	mustRegister(t, err)
	s := recorder.Snapshot()
	if len(s.Events) != 2 || s.Events[0].Artifact != "Item.Inner" || s.Events[1].Artifact != "Item.Outer" {
		t.Fatal(s)
	}
}
