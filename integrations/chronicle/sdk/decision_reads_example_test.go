// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk_test

import (
	"context"
	"fmt"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/projections"
	"github.com/cratis/chronicle.go/readmodels"
)

type RoomBooked struct {
	Guest string `json:"guest"`
}

type Room struct {
	ID    string `json:"id" chronicle:"key"`
	Guest string `json:"guest"`
}

type BookRoom struct {
	ID    integration.EventSourceID `json:"id"`
	Guest string                    `json:"guest"`
}

// ExampleEnableDecisions composes a protected booking decision. Build performs
// no I/O; executing BookRoom needs a running Chronicle kernel.
func ExampleEnableDecisions() {
	err := func() error {
		registry := chronicle.NewRegistry()
		booked, err := chronicle.RegisterEvent[RoomBooked](registry)
		if err != nil {
			return err
		}
		rooms, err := chronicle.RegisterReadModel[Room](registry)
		if err != nil {
			return err
		}
		if err := registry.AddProjection(projections.ModelBound(rooms, projections.FromEvent(booked))); err != nil {
			return err
		}
		client, err := chronicle.NewClient(chronicle.WithAppendOriginResolver(sdk.ResolveAppendOrigin), chronicle.WithRegistry(registry))
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()
		builder, err := arc.NewBuilder(arc.Options{})
		if err != nil {
			return err
		}
		adapter, err := sdk.New(client, sdk.Config{Store: "hotel"})
		if err != nil {
			return err
		}
		if err := adapter.Install(builder); err != nil {
			return err
		}
		decisions, err := sdk.EnableDecisions(builder, adapter)
		if err != nil {
			return err
		}
		err = commands.Register(builder.Commands(),
			commands.WithProtectedDecisions[BookRoom](),
			commands.WithoutModelValidation[BookRoom](),
			commands.WithNoResponse[BookRoom](),
			commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c BookRoom) (commands.Preparation[*sdk.Decision[Room]], error) {
				decision, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
				if err != nil {
					return commands.Preparation[*sdk.Decision[Room]]{}, err
				}
				return commands.Provided(decision), nil
			}, func(_ context.Context, _ *commands.Invocation, c BookRoom, decision *sdk.Decision[Room]) (RoomBooked, error) {
				// Arc verified the decision before Handle; a competing booking
				// of this room now rejects the whole commit.
				if room := decision.Instance(); room.Exists {
					return RoomBooked{}, validation.Reject(validation.Result{Severity: validation.Error, Message: "Room " + room.Value.ID + " is already booked."})
				}
				return RoomBooked{Guest: c.Guest}, nil
			}))
		if err != nil {
			return err
		}
		_, err = builder.Build()
		return err
	}()
	fmt.Println("built:", err == nil, err)
	// Output:
	// built: true <nil>
}
