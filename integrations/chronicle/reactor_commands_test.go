// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/identity"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/metadata"
)

func delivery(t *testing.T) c.Delivery {
	t.Helper()
	id, err := concepts.NewUUID()
	must(t, err)
	return c.Delivery{ID: "reactor#test#Default#event-log#source#0", Coordinates: c.Coordinates{Store: "test", Namespace: "Default", Sequence: "event-log"}, Correlation: id}
}
func TestReactorCommandsFreshRootsStopAtFirstFailureAndRetainEarlierCommit(t *testing.T) {
	f := &fakeFactory{result: c.CommitResult{Report: commands.CompletionReport{Disposition: commands.Committed}}}
	builder, integration := setup(t, f)
	must(t, integration.Install(builder))
	calls := 0
	must(t, commands.Register[Change](builder, commands.Handle(func(Change, context.Context) (Changed, error) { calls++; return Changed{}, nil }), commands.WithNoResponse[Change]()))
	must(t, commands.Register[Child](builder, commands.Void(func(Child, context.Context) error { t.Fatal("unauthorized child ran"); return nil }), commands.WithAuthorization[Child](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"missing"}}}})))
	app := start(t, builder)
	principal := identity.System("automation")
	bridge, err := c.NewReactorCommands(c.ReactorCommandOptions{Store: "test", Principal: &principal, Replay: c.LiveOnly})
	must(t, err)
	must(t, bridge.Bind(app))
	first := delivery(t)
	err = bridge.Execute(t.Context(), first, Change{ID: "first"}, Child{ID: "failed"}, Change{ID: "never"})
	var failure *c.CommandFailure
	if !errors.As(err, &failure) || failure.Result.IsAuthorized() || calls != 1 || f.commits != 1 {
		t.Fatal(err, calls, f)
	}
	second := delivery(t)
	must(t, bridge.Execute(t.Context(), second, Change{ID: "second"}))
	if calls != 2 || f.begins != 2 || f.commits != 2 {
		t.Fatal(calls, f)
	}
	if f.metadata[0].Actor.Subject != "[System]" {
		t.Fatal(f.metadata)
	}
}
func TestReactorReplayAndCoordinateRefusalBeforeHandler(t *testing.T) {
	f := &fakeFactory{}
	builder, integration := setup(t, f)
	must(t, integration.Install(builder))
	must(t, commands.Register[Change](builder, commands.Void(func(Change, context.Context) error { t.Fatal("replay/mismatched delivery ran handler"); return nil })))
	app := start(t, builder)
	principal := identity.System()
	bridge, err := c.NewReactorCommands(c.ReactorCommandOptions{Store: "test", Principal: &principal, Replay: c.LiveOnly})
	must(t, err)
	must(t, bridge.Bind(app))
	value := delivery(t)
	value.Replay = true
	must(t, bridge.Execute(t.Context(), value, Change{ID: "a"}))
	value.Replay = false
	value.Coordinates.Store = "another"
	if err := bridge.Execute(t.Context(), value, Change{}); !errors.Is(err, c.ErrMismatch) {
		t.Fatal(err)
	}
	value.Coordinates.Store = "test"
	value.Coordinates.Namespace = "other"
	if err := bridge.Execute(t.Context(), value, Change{}); !errors.Is(err, c.ErrMismatch) {
		t.Fatal(err)
	}
	if f.begins != 0 {
		t.Fatal(f)
	}
}
