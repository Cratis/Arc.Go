// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"fmt"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
)

type seatReservations interface {
	Reserve(context.Context, string, string) error
	CancelOwned(context.Context, string) error
}

type reserveSeat struct{ ReservationID, SeatID string }

func (reserveSeat) CommandOperation() {}
func (o reserveSeat) Execute(ctx context.Context, service seatReservations) error {
	return service.Reserve(ctx, o.ReservationID, o.SeatID)
}
func (o reserveSeat) Compensate(ctx context.Context, service seatReservations, _ commands.OperationFailure) error {
	return service.CancelOwned(ctx, o.ReservationID)
}

type bookSeat struct{ ReservationID, SeatID string }

func (c bookSeat) Handle(context.Context) (commands.Outcome[string], error) {
	return commands.Respond(c.ReservationID, reserveSeat(c)), nil
}

type reservationService struct{ calls int }

func (s *reservationService) Reserve(context.Context, string, string) error { s.calls++; return nil }
func (*reservationService) CancelOwned(context.Context, string) error       { return nil }

func ExampleRegisterOperation() {
	service := &reservationService{} // Application-owned, borrowed by this pipeline.
	var registry commands.Registry
	if err := commands.RegisterOperation[reserveSeat, seatReservations](&registry,
		func(context.Context, *execution.Scope) (seatReservations, error) { return service, nil }); err != nil {
		panic(err)
	}
	if err := commands.Register[bookSeat](&registry, commands.Handle(bookSeat.Handle), commands.WithOperations[bookSeat]()); err != nil {
		panic(err)
	}
	pipeline, err := registry.Build(commands.PipelineOptions{})
	if err != nil {
		panic(err)
	}
	command := bookSeat{"reservation-1", "seat-2"}
	_, _ = command.Handle(context.Background()) // Declares work only.
	fmt.Println("before pipeline:", service.calls)
	result, err := commands.Execute[string](context.Background(), pipeline, command)
	if err != nil {
		panic(err)
	}
	response, _ := result.Response()
	recovery, _ := result.Recovery()
	fmt.Println(response, result.IsSuccess(), service.calls, recovery.StartedCount)
	// Output:
	// before pipeline: 0
	// reservation-1 true 1 1
}
