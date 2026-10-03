// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func TestCancellationAfterCandidateReservationRestoresBothBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewState([]Item{{ID: 1, Name: "first"}}, observable.SubjectOptions[[]Item]{})
		mustRegister(t, err)
		pipeline := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		observation, _, err := pipeline.Open(ctx, "Item.Observe", queries.Request{})
		mustRegister(t, err)
		application, err := streaming.NewBudget(1 << 20)
		mustRegister(t, err)
		connection, err := streaming.NewBudget(1 << 20)
		mustRegister(t, err)
		reserved, releasePrepare, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- observation.Run(ctx, queries.ObservationOptions{TransferMode: queries.Delta, ReserveBaseline: func(bytes int64) (func(), error) {
				app, err := application.Acquire(bytes)
				if err != nil {
					return nil, err
				}
				conn, err := connection.Acquire(bytes)
				if err != nil {
					app.Release()
					return nil, err
				}
				close(reserved)
				<-releasePrepare // Cancel after reservation but before prepare returns.
				return func() { conn.Release(); app.Release() }, nil
			}}, func(queries.Result[any]) error {
				t.Error("canceled candidate reached delivery")
				return nil
			})
		}()
		<-reserved
		if application.Used() == 0 || connection.Used() == 0 {
			t.Fatal("candidate did not reserve both budgets")
		}
		cancel()
		close(releasePrepare)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if application.Used() != 0 || connection.Used() != 0 {
			t.Fatalf("leaked candidate: application %d, connection %d", application.Used(), connection.Used())
		}
		mustRegister(t, observation.Close(context.Background()))
		mustRegister(t, pipeline.CloseObservations(context.Background()))
	})
}
