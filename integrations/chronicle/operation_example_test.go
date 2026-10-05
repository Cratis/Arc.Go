// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	c "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/chronicle.go/eventsequences"
)

// SeatService is an application-owned external system.
type SeatService interface {
	Reserve(ctx context.Context, bookingID, seatID string) error
	Release(ctx context.Context, bookingID string) error
}

type SeatBooked struct {
	SeatID string `json:"seatId"`
}

type BookSeat struct {
	BookingID string `json:"bookingId"`
	SeatID    string `json:"seatId"`
	Passenger string `json:"passenger"`
}

// The operation and registration below are the documented excerpt.
type reserveSeat struct{ BookingID, SeatID string }

func (reserveSeat) CommandOperation() {}
func (o reserveSeat) Execute(ctx context.Context, seats SeatService) error {
	return seats.Reserve(ctx, o.BookingID, o.SeatID)
}
func (o reserveSeat) Compensate(ctx context.Context, seats SeatService, _ commands.OperationFailure) error {
	return seats.Release(ctx, o.BookingID)
}

type seatCatalog struct{}

func (seatCatalog) Descriptors() []c.EventDescriptor {
	return []c.EventDescriptor{{Type: reflect.TypeFor[SeatBooked](), Identity: c.EventType{ID: "seat-booked", Generation: 1}, Validate: func(v any) error { _, err := json.Marshal(v); return err }}}
}

type rejectingStore struct{}

func (rejectingStore) NewAppendOrigin() any { return eventsequences.NewOrigin() }
func (rejectingStore) Subscribe(context.Context, c.Coordinates, correlation.ID, func(c.CommitResult, error)) (func(), error) {
	return func() {}, nil
}
func (s rejectingStore) Begin(context.Context, c.Coordinates) (c.Participant, c.CompletionOwner, error) {
	return s, s, nil
}
func (rejectingStore) Stage(context.Context, c.Batch) error { return nil }
func (rejectingStore) Commit(context.Context) (c.CommitResult, error) {
	return c.CommitResult{Report: commands.CompletionReport{Disposition: commands.NotCommitted}}, nil
}
func (rejectingStore) Rollback() error { return nil }

type printedSeats struct{}

func (printedSeats) Reserve(_ context.Context, bookingID, seatID string) error {
	fmt.Println("reserve", bookingID, seatID)
	return nil
}
func (printedSeats) Release(_ context.Context, bookingID string) error {
	fmt.Println("release", bookingID)
	return nil
}

// A rejected Chronicle commit is NotCommitted, so the reserved seat is released.
func Example_operationCompensation() {
	var seats SeatService = printedSeats{}
	store := rejectingStore{}
	builder, _ := arc.NewBuilder(arc.Options{})
	integration, _ := c.New(c.Options{StoreResolver: func(context.Context, commands.CommandContext) (c.Coordinates, error) {
		return c.Coordinates{Store: "bookings", Namespace: "Default"}, nil
	}, Transactions: store, Events: seatCatalog{}, Appends: store})
	if err := integration.Install(builder); err != nil {
		panic(err)
	}
	err := commands.RegisterOperation[reserveSeat, SeatService](builder.Commands(),
		func(context.Context, *execution.Scope) (SeatService, error) { return seats, nil })
	if err != nil {
		panic(err)
	}
	err = commands.Register[BookSeat](builder, commands.Handle(func(c BookSeat, _ context.Context) (commands.Outcome[commands.NoResponse], error) {
		return commands.Effects[commands.NoResponse](SeatBooked{SeatID: c.SeatID}, reserveSeat{c.BookingID, c.SeatID}), nil
	}), commands.WithOperations[BookSeat]())
	if err != nil {
		panic(err)
	}
	app, err := builder.Build()
	if err != nil {
		panic(err)
	}
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
	defer func() { _ = app.Shutdown(context.Background()) }()
	result, _ := app.Commands().Execute(context.Background(), BookSeat{BookingID: "booking-1", SeatID: "12A"})
	recovery, _ := result.Recovery()
	fmt.Println(result.Completion().Disposition == commands.NotCommitted, recovery.Status == commands.RecoveryCompleted)
	// Output:
	// reserve booking-1 12A
	// release booking-1
	// true true
}
