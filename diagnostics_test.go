// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/queries"
)

type diagnosticResources struct{}

func (diagnosticResources) Close(context.Context) error { return errors.New("SECRET cleanup") }

func TestDiagnosticsAdmittedPipelinesCountPreflightAndPreserveCleanupOutcome(t *testing.T) {
	recorder, err := observability.NewRecorder(observability.Options{})
	if err != nil {
		t.Fatal(err)
	}
	builder, err := arc.NewBuilder(arc.Options{Diagnostics: recorder, OpenResources: func(context.Context) (execution.Resources, error) { return diagnosticResources{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[builderCommand](builder); err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[builderModel](builder, "Current", queries.Function(func(context.Context, queries.NoArguments) (builderModel, error) {
		return builderModel{Name: "SECRET"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	app, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = commands.Execute[commands.NoResponse](t.Context(), app.Commands(), builderCommand{})
	if err == nil {
		t.Fatal("admitted before start")
	}
	_, err = queries.Perform[builderModel](t.Context(), app.Queries(), "builderModel.Current", queries.Request{})
	if err == nil {
		t.Fatal("admitted before start")
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	_, err = commands.Execute[commands.NoResponse](t.Context(), app.Commands(), builderCommand{})
	if err == nil {
		t.Fatal("lost cleanup error")
	}
	_, err = queries.Perform[builderModel](t.Context(), app.Queries(), "builderModel.Current", queries.Request{})
	if err == nil {
		t.Fatal("lost cleanup error")
	}
	snapshot := recorder.Snapshot()
	if len(snapshot.Events) != 4 {
		t.Fatal(snapshot)
	}
	for _, event := range snapshot.Events {
		if event.Outcome != observability.Error {
			t.Fatal("normal admission release was classified as cancellation", event)
		}
	}
}
