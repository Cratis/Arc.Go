// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"fmt"
	"reflect"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
)

type BookRoom struct{ Room string }

type Room struct{ Booked bool }

// roomSource stands in for a provider integration such as Chronicle's. Real
// evidence is a provider token; Enroll registers it with the completion owner.
type roomSource struct{}

func (roomSource) Admit(context.Context) error          { return nil }
func (roomSource) Acquire(context.Context) (any, error) { return &Room{}, nil }
func (roomSource) Check(context.Context, any) error     { return nil }
func (roomSource) Enroll(context.Context, any) error    { return nil }

// noOwner is a completion owner that persists nothing.
type noOwner struct{}

func (noOwner) Begin(context.Context, *commands.Invocation) error { return nil }
func (noOwner) Complete(context.Context, *commands.Invocation, commands.Result[any]) (commands.CompletionReport, error) {
	return commands.CompletionReport{Disposition: commands.NoPersistedWork}, nil
}

func ExampleWithProtectedDecisions() {
	var registry commands.Registry
	provider := commands.NewDecisionProvider()
	err := commands.Register(&registry,
		commands.WithProtectedDecisions[BookRoom](),
		commands.WithoutModelValidation[BookRoom](),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c BookRoom) (commands.Preparation[*commands.DecisionRead], error) {
			target := commands.DecisionTarget{Provider: provider, Model: reflect.TypeFor[Room](), Store: "hotel", Namespace: "default", Key: c.Room}
			read, err := commands.ReadDecision(ctx, inv, target, roomSource{})
			if err != nil {
				return commands.Preparation[*commands.DecisionRead]{}, err
			}
			return commands.Provided(read), nil
		}, func(_ context.Context, _ *commands.Invocation, c BookRoom, read *commands.DecisionRead) (commands.NoResponse, error) {
			// Arc verified read before Handle; decide from it.
			fmt.Println("booking", c.Room, "booked:", read.Value().(*Room).Booked)
			return commands.NoResponse{}, nil
		}))
	if err == nil {
		err = registry.AddDecisionProvider(provider)
	}
	if err == nil {
		err = registry.AddDeferredCommitParticipant("owner", func(context.Context, *execution.Scope) (commands.DeferredCommitParticipant, error) {
			return noOwner{}, nil
		})
	}
	if err != nil {
		fmt.Println(err)
		return
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	result, err := pipeline.Execute(context.Background(), BookRoom{Room: "101"})
	fmt.Println(result.IsSuccess(), err)
	// Output:
	// booking 101 booked: false
	// true <nil>
}
