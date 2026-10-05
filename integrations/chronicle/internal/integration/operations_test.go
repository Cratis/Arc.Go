//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/constraints"
	"github.com/cratis/chronicle.go/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// SeatReleased is the compensating fact. Compensation is a new write, never an
// unappend or rollback of the original command.
type SeatReleased struct {
	Seat string `json:"seat"`
}

// seatServices is the complete execution/compensation bundle. The store and its
// sequence are borrowed SDK facades that remain usable while the client is open.
type seatServices struct {
	store *chronicle.EventStore
	lost  *atomic.Bool
	mu    sync.Mutex
	steps []string
}

func (s *seatServices) record(step string) {
	s.mu.Lock()
	s.steps = append(s.steps, step)
	s.mu.Unlock()
}
func (s *seatServices) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

type reserveSeatOperation struct {
	Seat      string
	Immediate string // Name of an immediately appended AuthorCreated, if any.
	LoseAck   bool
	Fail      bool
}

var errReservationFailed = errors.New("reservation failed")

func (reserveSeatOperation) CommandOperation() {}
func (o reserveSeatOperation) Execute(ctx context.Context, s *seatServices) error {
	s.record("execute")
	if o.Immediate != "" {
		sequence, err := s.store.EventSequence(events.EventLog)
		if err != nil {
			return err
		}
		if o.LoseAck {
			s.lost.Store(true)
		}
		result, err := sequence.Append(ctx, events.SourceID(o.Seat), AuthorCreated{Name: o.Immediate})
		if err == nil {
			err = result.Err()
		}
		if err != nil {
			return err
		}
	}
	if o.Fail {
		return errReservationFailed
	}
	return nil
}
func (o reserveSeatOperation) Compensate(ctx context.Context, s *seatServices, failure commands.OperationFailure) error {
	s.record("compensate")
	if failure.Completion.Disposition != commands.NoPersistedWork && failure.Completion.Disposition != commands.NotCommitted {
		return errors.New("compensation entered with hazardous persistence facts")
	}
	// The retained facade is called after the command's terminal step completed.
	result, err := s.store.EventLog().Append(ctx, events.SourceID("released-"+o.Seat), SeatReleased{Seat: o.Seat})
	if err != nil {
		return err
	}
	return result.Err()
}

type BookSeat struct {
	Seat             string                    `json:"seat"`
	ID               integration.EventSourceID `json:"id"`
	Name             string                    `json:"name"`
	Operation        reserveSeatOperation      `json:"-"`
	HandlerImmediate string                    `json:"-"`
	HandlerLoseAck   bool                      `json:"-"`
}

func seatRegistry(t *testing.T) func(*chronicle.Registry) {
	return func(registry *chronicle.Registry) {
		event, err := chronicle.RegisterEvent[AuthorCreated](registry)
		require(t, err)
		_, err = chronicle.RegisterEvent[SeatReleased](registry)
		require(t, err)
		constraint, err := constraints.UniqueValues("unique-author-name").On(event.Descriptor(), "name").Build()
		require(t, err)
		require(t, registry.AddConstraint(constraint))
	}
}

// lossyClient forwards an armed append, then replaces its acknowledgement with a
// transport error. The kernel has persisted the event; the caller cannot know.
func lossyClient(t *testing.T, register func(*chronicle.Registry)) (*chronicle.Client, *atomic.Bool) {
	t.Helper()
	endpoint := os.Getenv("CHRONICLE_INTEGRATION_CONNECTION_STRING")
	parsed, err := url.Parse(endpoint)
	require(t, err)
	lost := &atomic.Bool{}
	interceptor := func(ctx context.Context, method string, request, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
		if err := invoke(ctx, method, request, reply, conn, options...); err != nil {
			return err
		}
		if method == "/Cratis.Chronicle.Contracts.Sequences.EventSequences/Append" && lost.CompareAndSwap(true, false) {
			return status.Error(codes.Unavailable, "acknowledgement lost after dispatch")
		}
		return nil
	}
	// Loopback development kernel with its self-signed certificate only.
	conn, err := grpc.NewClient(parsed.Host, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true})), grpc.WithUnaryInterceptor(interceptor)) // #nosec G402
	require(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	registry := chronicle.NewRegistry()
	register(registry)
	client, err := chronicle.NewClient(chronicle.WithGRPCConnection(conn), chronicle.WithAppendOriginResolver(sdk.ResolveAppendOrigin), chronicle.WithRegistry(registry), chronicle.WithConnectionString(endpoint), chronicle.WithDevelopmentDefaults())
	require(t, err)
	t.Cleanup(func() { require(t, client.Close()) })
	return client, lost
}

func seatApp(t *testing.T, ctx context.Context, client *chronicle.Client, store chronicle.StoreName, lost *atomic.Bool) (*arc.Application, *seatServices) {
	t.Helper()
	handle, err := client.EventStore(ctx, store)
	require(t, err)
	services := &seatServices{store: handle, lost: lost}
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: store})
	require(t, err)
	require(t, adapter.Install(builder))
	require(t, commands.RegisterOperation[reserveSeatOperation, *seatServices](builder.Commands(), func(context.Context, *execution.Scope) (*seatServices, error) {
		return services, nil
	}))
	require(t, commands.Register[BookSeat](builder, commands.Handle(func(command BookSeat, ctx context.Context) (commands.Outcome[commands.NoResponse], error) {
		if command.HandlerImmediate != "" {
			if command.HandlerLoseAck {
				services.lost.Store(true)
			}
			// Deliberately ignore the result: observation must still prohibit
			// operation entry when this handler has already caused persistence.
			_, _ = services.store.EventLog().Append(ctx, events.SourceID(command.Seat), AuthorCreated{Name: command.HandlerImmediate})
		}
		effects := []any{command.Operation}
		if command.Name != "" {
			effects = append(effects, AuthorCreated{Name: command.Name})
		}
		return commands.Effects[commands.NoResponse](effects...), nil
	}), commands.WithOperations[BookSeat]()))
	app, err := builder.Build()
	require(t, err)
	require(t, app.Start(ctx))
	t.Cleanup(func() { require(t, app.Shutdown(context.Background())) })
	return app, services
}

func names(t *testing.T, appended []events.Appended) []string {
	t.Helper()
	var result []string
	for _, value := range appended {
		var event struct {
			Name string `json:"name"`
			Seat string `json:"seat"`
		}
		require(t, json.Unmarshal(value.Content, &event))
		result = append(result, event.Name+event.Seat)
	}
	return result
}

func TestOperationsAgainstTheKernelFollowCommitAwareRecovery(t *testing.T) {
	client, lost := lossyClient(t, seatRegistry(t))
	_, store, ctx := clientFor(t, func(*chronicle.Registry) {})
	app, services := seatApp(t, ctx, client, store, lost)
	seed, err := services.store.EventLog().Append(ctx, "seed", AuthorCreated{Name: "taken"})
	require(t, err)
	require(t, seed.Err())

	for _, scenario := range []struct {
		name        string
		command     BookSeat
		success     bool
		disposition commands.CommitDisposition
		recovery    commands.RecoveryStatus
		steps       int
		booked      []string // Expected history of the command's own source.
		immediate   []string // Expected history of the operation's immediate source.
		released    bool
	}{
		{name: "no persisted work", command: BookSeat{Seat: "s1", ID: "c1", Operation: reserveSeatOperation{Seat: "s1", Fail: true}},
			disposition: commands.NoPersistedWork, recovery: commands.RecoveryCompleted, steps: 2, released: true},
		{name: "deferred constraint rejection", command: BookSeat{Seat: "s2", ID: "c2", Name: "taken", Operation: reserveSeatOperation{Seat: "s2"}},
			disposition: commands.NotCommitted, recovery: commands.RecoveryCompleted, steps: 2, released: true},
		{name: "confirmed immediate append", command: BookSeat{Seat: "s3", ID: "c3", Operation: reserveSeatOperation{Seat: "s3", Immediate: "immediate-3", Fail: true}},
			disposition: commands.Committed, recovery: commands.RecoverySuppressed, steps: 1, immediate: []string{"immediate-3"}},
		{name: "lost immediate acknowledgement", command: BookSeat{Seat: "s4", ID: "c4", Operation: reserveSeatOperation{Seat: "s4", Immediate: "immediate-4", LoseAck: true}},
			disposition: commands.OutcomeUnknown, recovery: commands.RecoveryIndeterminate, steps: 1, immediate: []string{"immediate-4"}},
		{name: "confirmed immediate and rejected deferred", command: BookSeat{Seat: "s5", ID: "c5", Name: "taken", Operation: reserveSeatOperation{Seat: "s5", Immediate: "immediate-5"}},
			disposition: commands.MixedCommit, recovery: commands.RecoveryIndeterminate, steps: 1, immediate: []string{"immediate-5"}},
		{name: "committed deferred work", command: BookSeat{Seat: "s6", ID: "c6", Name: "booked-6", Operation: reserveSeatOperation{Seat: "s6"}},
			success: true, disposition: commands.Committed, recovery: commands.RecoveryNotNeeded, steps: 1, booked: []string{"booked-6"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			before := len(services.recorded())
			result, err := app.Commands().Execute(ctx, scenario.command)
			if result.IsSuccess() != scenario.success || (err == nil) != scenario.success {
				t.Fatal(result.Details(), err)
			}
			if result.Completion().Disposition != scenario.disposition {
				t.Fatalf("completion = %v, want %v (%v)", result.Completion().Disposition, scenario.disposition, err)
			}
			summary, present := result.Recovery()
			if !present || summary.Status != scenario.recovery || summary.StartedCount != 1 {
				t.Fatal(summary, result.OperationOutcomes())
			}
			if steps := services.recorded()[before:]; len(steps) != scenario.steps {
				t.Fatal("unexpected operation callbacks", steps)
			}
			if outcomes := result.OperationOutcomes(); len(outcomes) != 1 || outcomes[0].CompensationFailure != nil {
				t.Fatal(outcomes)
			}
			if got := names(t, history(t, ctx, client, store, "Default", string(scenario.command.ID))); len(got) != len(scenario.booked) || (len(got) == 1 && got[0] != scenario.booked[0]) {
				t.Fatal("command source history", got)
			}
			// Readback through the same client is unfaulted after a lost acknowledgement.
			if got := names(t, history(t, ctx, client, store, "Default", scenario.command.Seat)); len(got) != len(scenario.immediate) || (len(got) == 1 && got[0] != scenario.immediate[0]) {
				t.Fatal("immediate history", got)
			}
			if got := history(t, ctx, client, store, "Default", "released-"+scenario.command.Seat); (len(got) == 1) != scenario.released || len(got) > 1 {
				t.Fatal("compensating write", names(t, got))
			}
		})
	}
	if lost.Load() {
		t.Fatal("lost-acknowledgement interceptor remained armed")
	}
	if findings := mustReject(t, ctx, app, BookSeat{Seat: "s7", ID: "c7", Name: "taken", Operation: reserveSeatOperation{Seat: "s7"}}); findings[0].Reason != validation.ConstraintViolation {
		t.Fatal(findings)
	}
}

func TestHandlerImmediateAppendsRefuseOperationsBeforeEntryAgainstTheKernel(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		loseAck     bool
		disposition commands.CommitDisposition
	}{
		{"confirmed", false, commands.Committed},
		{"lost acknowledgement", true, commands.OutcomeUnknown},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client, lost := lossyClient(t, seatRegistry(t))
			_, store, ctx := clientFor(t, func(*chronicle.Registry) {})
			app, services := seatApp(t, ctx, client, store, lost)
			result, err := app.Commands().Execute(ctx, BookSeat{Seat: "handler-seat", ID: "handler-command",
				HandlerImmediate: "handler-booked", HandlerLoseAck: scenario.loseAck,
				Operation: reserveSeatOperation{Seat: "handler-seat"}})
			if result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) || result.Completion().Disposition != scenario.disposition {
				t.Fatal(result.Details(), result.Completion(), err)
			}
			if steps := services.recorded(); len(steps) != 0 {
				t.Fatal("operation entered after handler persistence", steps)
			}
			if summary, present := result.Recovery(); !present || summary.StartedCount != 0 {
				t.Fatal(summary, present)
			}
			if got := names(t, history(t, ctx, client, store, "Default", "handler-seat")); len(got) != 1 || got[0] != "handler-booked" {
				t.Fatal("handler immediate history", got)
			}
			if lost.Load() {
				t.Fatal("lost-acknowledgement interceptor remained armed")
			}
		})
	}
}

func mustReject(t *testing.T, ctx context.Context, app *arc.Application, command BookSeat) []validation.Result {
	t.Helper()
	result, err := app.Commands().Execute(ctx, command)
	if result.IsSuccess() || err == nil || len(result.Details().ValidationResults) == 0 {
		t.Fatal(result.Details(), err)
	}
	return result.Details().ValidationResults
}

func TestOperationAttemptsAndTenantsAreIsolatedAgainstTheKernel(t *testing.T) {
	client, lost := lossyClient(t, seatRegistry(t))
	_, store, ctx := clientFor(t, func(*chronicle.Registry) {})
	app, _ := seatApp(t, ctx, client, store, lost)
	committed, err := app.Commands().Execute(ctx, BookSeat{Seat: "a1", ID: "attempt-1", Operation: reserveSeatOperation{Seat: "a1", Immediate: "attempt-1", Fail: true}})
	if committed.IsSuccess() || err == nil || committed.Completion().Disposition != commands.Committed {
		t.Fatal(committed.Details(), err)
	}
	// A fresh attempt has a fresh origin: earlier immediate facts never leak.
	retried, err := app.Commands().Execute(ctx, BookSeat{Seat: "a2", ID: "attempt-2", Operation: reserveSeatOperation{Seat: "a2", Fail: true}})
	summary, _ := retried.Recovery()
	if retried.IsSuccess() || !errors.Is(err, errReservationFailed) || retried.Completion().Disposition != commands.NoPersistedWork || summary.Status != commands.RecoveryCompleted {
		t.Fatal(retried.Details(), retried.Completion(), summary, err)
	}
	tenant, err := tenancy.ParseID("other")
	require(t, err)
	booked, err := app.Commands().Execute(tenancy.WithTenant(ctx, tenant), BookSeat{Seat: "t1", ID: "tenant", Name: "tenant-booking", Operation: reserveSeatOperation{Seat: "t1"}})
	require(t, err)
	if !booked.IsSuccess() || len(history(t, ctx, client, store, "other", "tenant")) != 1 || len(history(t, ctx, client, store, "Default", "tenant")) != 0 {
		t.Fatal(booked.Details())
	}
}

type EarlyCommitSeat struct {
	ID integration.EventSourceID `json:"id"`
}

func TestExplicitAggregateCommitIsRefusedForOperationsAgainstTheKernel(t *testing.T) {
	client, lost := lossyClient(t, seatRegistry(t))
	_, store, ctx := clientFor(t, func(*chronicle.Registry) {})
	handle, err := client.EventStore(ctx, store)
	require(t, err)
	services := &seatServices{store: handle, lost: lost}
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: store})
	require(t, err)
	require(t, adapter.Install(builder))
	require(t, commands.RegisterOperation[reserveSeatOperation, *seatServices](builder.Commands(), func(context.Context, *execution.Scope) (*seatServices, error) {
		return services, nil
	}))
	authors, err := integration.DefineAggregate(func(root *integration.AggregateRoot) *AuthorAggregate {
		return &AuthorAggregate{AggregateRoot: root}
	}, integration.OnAggregateEvent(func(a *AuthorAggregate, e AuthorCreated) error { a.Name = e.Name; return nil }))
	require(t, err)
	var refusal error
	require(t, commands.Register[EarlyCommitSeat](builder, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ EarlyCommitSeat) (commands.Operations, error) {
		author, err := authors.Get(ctx, inv)
		if err != nil {
			return commands.Operations{}, err
		}
		if err := author.Apply(ctx, AuthorCreated{Name: "early"}); err != nil {
			return commands.Operations{}, err
		}
		_, refusal = author.Commit(ctx) // Deliberately ignored: the refusal is sticky.
		return commands.NewOperations(reserveSeatOperation{Seat: "early"})
	}), commands.WithOperations[EarlyCommitSeat]()))
	app, err := builder.Build()
	require(t, err)
	require(t, app.Start(ctx))
	t.Cleanup(func() { require(t, app.Shutdown(context.Background())) })
	result, err := app.Commands().Execute(ctx, EarlyCommitSeat{ID: "early"})
	if !errors.Is(refusal, commands.ErrInvalidOperation) || result.IsSuccess() || !errors.Is(err, commands.ErrInvalidOperation) {
		t.Fatal(refusal, result.Details(), err)
	}
	if _, present := result.Response(); present || len(services.recorded()) != 0 || len(history(t, ctx, client, store, "Default", "early")) != 0 {
		t.Fatal("refused operation command persisted or entered operations", services.recorded())
	}
}
