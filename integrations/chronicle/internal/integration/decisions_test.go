//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/events"
	"github.com/cratis/chronicle.go/projections"
	"github.com/cratis/chronicle.go/readmodels"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

type RoomBooked struct {
	Guest string `json:"guest"`
}
type LedgerEntered struct {
	Room string `json:"room"`
}
type Room struct {
	ID    string `json:"id" chronicle:"key"`
	Guest string `json:"guest"`
}
type GuestRoom struct {
	ID    string `json:"id" chronicle:"key"`
	Guest string `json:"guest"`
}

// BookRoom decides from a protected read of Room. Compete appends a competing
// booking directly after the read; LoseAck drops the commit acknowledgement.
type BookRoom struct {
	ID      integration.EventSourceID `json:"id"`
	Guest   string                    `json:"guest"`
	Compete bool                      `json:"-"`
	LoseAck bool                      `json:"-"`
}
type InspectRoom struct {
	ID integration.EventSourceID `json:"id"`
}
type InspectGuestRoom struct {
	ID integration.EventSourceID `json:"id"`
}
type PeekRoom struct {
	ID integration.EventSourceID `json:"id"`
}
type GlanceRoom struct {
	ID integration.EventSourceID `json:"id"`
}
type InspectSuite struct {
	ID integration.EventSourceID `json:"id"`
}
type Suite struct {
	ID string `json:"id" chronicle:"key"`
}

type roomFixture struct {
	app     *arc.Application
	client  *chronicle.Client
	store   chronicle.StoreName
	ctx     context.Context
	lost    *atomic.Bool
	handled atomic.Int32

	mu sync.Mutex
	// filterReads records BookRoom decisions read by a filter, which also runs
	// in validation-only execution, keyed by validation-only mode.
	filterReads map[bool][]*commands.DecisionRead
	// providedReads records the reads BookRoom's Provide returned.
	providedReads []*commands.DecisionRead
}

func roomRegistry(t *testing.T, rooms *readmodels.Model[Room], guests *readmodels.Model[GuestRoom]) func(*chronicle.Registry) {
	return func(registry *chronicle.Registry) {
		booked, err := chronicle.RegisterEvent[RoomBooked](registry)
		require(t, err)
		_, err = chronicle.RegisterEvent[LedgerEntered](registry)
		require(t, err)
		*rooms, err = chronicle.RegisterReadModel[Room](registry)
		require(t, err)
		require(t, registry.AddProjection(projections.ModelBound(*rooms, projections.FromEvent(booked))))
		*guests, err = chronicle.RegisterReadModel[GuestRoom](registry, readmodels.WithPII("guest"))
		require(t, err)
		require(t, registry.AddProjection(projections.ModelBound(*guests, projections.FromEvent(booked))))
	}
}

// batchLossyClient forwards an armed event-sequence append of any shape, then
// replaces its acknowledgement with a transport error after the kernel persisted it.
func batchLossyClient(t *testing.T, register func(*chronicle.Registry)) (*chronicle.Client, *atomic.Bool) {
	t.Helper()
	endpoint := os.Getenv("CHRONICLE_INTEGRATION_CONNECTION_STRING")
	parsed, err := url.Parse(endpoint)
	require(t, err)
	lost := &atomic.Bool{}
	interceptor := func(ctx context.Context, method string, request, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
		if err := invoke(ctx, method, request, reply, conn, options...); err != nil {
			return err
		}
		if strings.HasPrefix(method, "/Cratis.Chronicle.Contracts.Sequences.EventSequences/Append") && lost.CompareAndSwap(true, false) {
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

func roomApp(t *testing.T) *roomFixture {
	t.Helper()
	var rooms readmodels.Model[Room]
	var guests readmodels.Model[GuestRoom]
	client, lost := batchLossyClient(t, roomRegistry(t, &rooms, &guests))
	_, store, ctx := clientFor(t, func(*chronicle.Registry) {})
	f := &roomFixture{client: client, store: store, ctx: ctx, lost: lost}
	builder, err := arc.NewBuilder(arc.Options{})
	require(t, err)
	adapter, err := sdk.New(client, sdk.Config{Store: store})
	require(t, err)
	require(t, adapter.Install(builder))
	decisions, err := sdk.EnableDecisions(builder, adapter)
	require(t, err)
	handle, err := client.EventStore(ctx, store)
	require(t, err)
	f.filterReads = map[bool][]*commands.DecisionRead{}
	require(t, builder.Commands().AddFilter("rooms.decide", func(context.Context, *execution.Scope) (commands.Filter, error) {
		return commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			id := inv.CommandContext().CorrelationID()
			c, ok := inv.CommandContext().Command().(BookRoom)
			if !ok {
				return commands.Success(id), nil
			}
			decision, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
			if err != nil {
				return commands.Success(id), err
			}
			f.mu.Lock()
			validationOnly := inv.CommandContext().IsValidationOnly()
			f.filterReads[validationOnly] = append(f.filterReads[validationOnly], decision.DecisionReads()...)
			f.mu.Unlock()
			return commands.Success(id), nil
		}), nil
	}))
	require(t, commands.Register(builder.Commands(),
		commands.WithProtectedDecisions[BookRoom](),
		commands.WithoutModelValidation[BookRoom](),
		commands.WithNoResponse[BookRoom](),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c BookRoom) (commands.Preparation[*sdk.Decision[Room]], error) {
			decision, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
			if err != nil {
				return commands.Preparation[*sdk.Decision[Room]]{}, err
			}
			f.mu.Lock()
			f.providedReads = append(f.providedReads, decision.DecisionReads()...)
			f.mu.Unlock()
			return commands.Provided(decision), nil
		}, func(_ context.Context, _ *commands.Invocation, c BookRoom, decision *sdk.Decision[Room]) (integration.EventBatch, error) {
			f.handled.Add(1)
			if !decision.IsProtected() {
				t.Error("protected command received an advisory decision")
			}
			if room := decision.Instance(); room.Exists {
				return integration.EventBatch{}, validation.Reject(validation.Result{Severity: validation.Error, Message: "Room is already booked by " + room.Value.Guest + "."})
			}
			if c.Compete {
				// Another writer: the command's context would attribute the
				// append to this command as an immediate commit.
				result, err := handle.EventLog().Append(f.ctx, events.SourceID(c.ID), RoomBooked{Guest: "competitor"})
				require(t, err)
				require(t, result.Err())
			}
			if c.LoseAck {
				f.lost.Store(true)
			}
			return integration.EventsWithScopes([]integration.EventValue{
				integration.EventForSource(c.ID, RoomBooked{Guest: c.Guest}),
				integration.EventForSource("ledger-"+c.ID, LedgerEntered{Room: string(c.ID)}),
			}), nil
		})))
	require(t, commands.Register(builder.Commands(),
		commands.WithProtectedDecisions[InspectRoom](),
		commands.WithoutModelValidation[InspectRoom](),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c InspectRoom) (commands.Preparation[*sdk.Decision[Room]], error) {
			decision, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
			if err != nil {
				return commands.Preparation[*sdk.Decision[Room]]{}, err
			}
			// A second read of the same target shares the issued read.
			again, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
			if err != nil {
				return commands.Preparation[*sdk.Decision[Room]]{}, err
			}
			if again.DecisionReads()[0] != decision.DecisionReads()[0] {
				t.Error("repeated read issued a second decision")
			}
			return commands.Provided(decision), nil
		}, func(_ context.Context, _ *commands.Invocation, _ InspectRoom, decision *sdk.Decision[Room]) (string, error) {
			return decision.Instance().Value.Guest, nil
		})))
	require(t, commands.Register(builder.Commands(),
		commands.WithProtectedDecisions[InspectGuestRoom](),
		commands.WithoutModelValidation[InspectGuestRoom](),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c InspectGuestRoom) (commands.Preparation[*sdk.Decision[GuestRoom]], error) {
			decision, err := sdk.ReadDecision(ctx, inv, decisions, guests, readmodels.Key(c.ID))
			if err != nil {
				return commands.Preparation[*sdk.Decision[GuestRoom]]{}, err
			}
			return commands.Provided(decision), nil
		}, func(context.Context, *commands.Invocation, InspectGuestRoom, *sdk.Decision[GuestRoom]) (commands.NoResponse, error) {
			t.Error("classified decision reached Handle")
			return commands.NoResponse{}, nil
		})))
	require(t, commands.Register(builder.Commands(),
		commands.WithUnprotectedDecisions[PeekRoom](),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c PeekRoom) (commands.Preparation[*sdk.Decision[Room]], error) {
			decision, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
			if err != nil {
				return commands.Preparation[*sdk.Decision[Room]]{}, err
			}
			return commands.Provided(decision), nil
		}, func(_ context.Context, _ *commands.Invocation, _ PeekRoom, decision *sdk.Decision[Room]) (bool, error) {
			return decision.IsProtected() || decision.Instance().Exists, nil
		})))
	require(t, commands.Register(builder.Commands(),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c GlanceRoom) (commands.Preparation[*sdk.Decision[Room]], error) {
			decision, err := sdk.ReadDecision(ctx, inv, decisions, rooms, readmodels.Key(c.ID))
			if err != nil {
				return commands.Preparation[*sdk.Decision[Room]]{}, err
			}
			return commands.Provided(decision), nil
		}, func(context.Context, *commands.Invocation, GlanceRoom, *sdk.Decision[Room]) (commands.NoResponse, error) {
			t.Error("unmarked decision reached Handle")
			return commands.NoResponse{}, nil
		})))
	suites, err := readmodels.Define[Suite]()
	require(t, err)
	require(t, commands.Register(builder.Commands(),
		commands.WithProtectedDecisions[InspectSuite](),
		commands.WithoutModelValidation[InspectSuite](),
		commands.Prepare(func(ctx context.Context, inv *commands.Invocation, c InspectSuite) (commands.Preparation[*sdk.Decision[Suite]], error) {
			decision, err := sdk.ReadDecision(ctx, inv, decisions, suites, readmodels.Key(c.ID))
			if err != nil {
				return commands.Preparation[*sdk.Decision[Suite]]{}, err
			}
			return commands.Provided(decision), nil
		}, func(context.Context, *commands.Invocation, InspectSuite, *sdk.Decision[Suite]) (commands.NoResponse, error) {
			t.Error("unregistered decision reached Handle")
			return commands.NoResponse{}, nil
		})))
	f.app, err = builder.Build()
	require(t, err)
	require(t, f.app.Start(ctx))
	t.Cleanup(func() { require(t, f.app.Shutdown(context.Background())) })
	return f
}

func (f *roomFixture) guests(t *testing.T, source string) []string {
	t.Helper()
	var guests []string
	for _, value := range history(t, f.ctx, f.client, f.store, "Default", source) {
		guests = append(guests, string(value.Content))
	}
	return guests
}

func TestProtectedDecisionCommitsAndRefusesAStaleDecisionAgainstTheKernel(t *testing.T) {
	f := roomApp(t)
	result, err := f.app.Commands().Execute(f.ctx, BookRoom{ID: "101", Guest: "Ada"})
	if !result.IsSuccess() || err != nil || result.Completion().Disposition != commands.Committed {
		t.Fatal(result.Details(), result.Completion(), err)
	}
	if got := f.guests(t, "101"); len(got) != 1 || len(f.guests(t, "ledger-101")) != 1 {
		t.Fatal("booking history", got)
	}
	// The protected read folds the event log directly, so the next decision
	// sees the booking without waiting for materialization.
	result, err = f.app.Commands().Execute(f.ctx, BookRoom{ID: "101", Guest: "Grace"})
	if result.IsSuccess() || err == nil || len(result.Details().ValidationResults) != 1 || result.Details().ValidationResults[0].Message != "Room is already booked by Ada." {
		t.Fatal(result.Details(), err)
	}
	if got := f.guests(t, "101"); len(got) != 1 {
		t.Fatal("rejected decision appended", got)
	}
	inspected, err := f.app.Commands().Execute(f.ctx, InspectRoom{ID: "101"})
	if !inspected.IsSuccess() || err != nil || inspected.Completion().Disposition != commands.NoPersistedWork {
		t.Fatal(inspected.Details(), inspected.Completion(), err)
	}
	if value, _ := inspected.Response(); value != "Ada" {
		t.Fatal("decision value", value)
	}
}

func TestCompetingAppendRejectsTheWholeDecisionBatchAgainstTheKernel(t *testing.T) {
	f := roomApp(t)
	result, err := f.app.Commands().Execute(f.ctx, BookRoom{ID: "202", Guest: "Ada", Compete: true})
	if result.IsSuccess() || err == nil || result.Completion().Disposition != commands.NotCommitted {
		t.Fatal(result.Details(), result.Completion(), err)
	}
	findings := result.Details().ValidationResults
	if len(findings) == 0 || findings[0].Reason != validation.ConcurrencyViolation {
		t.Fatal("competing append was not reported as a concurrency violation", findings, err)
	}
	if got := f.guests(t, "202"); len(got) != 1 {
		t.Fatal("decided booking appended beside the competitor", got)
	}
	if got := f.guests(t, "ledger-202"); len(got) != 0 {
		t.Fatal("batch was partially committed", got)
	}
}

func TestLostDecisionCommitAcknowledgementStaysUnknownAgainstTheKernel(t *testing.T) {
	f := roomApp(t)
	result, err := f.app.Commands().Execute(f.ctx, BookRoom{ID: "303", Guest: "Ada", LoseAck: true})
	if result.IsSuccess() || err == nil || result.Completion().Disposition != commands.OutcomeUnknown {
		t.Fatal(result.Details(), result.Completion(), err)
	}
	if f.lost.Load() {
		t.Fatal("lost-acknowledgement interceptor remained armed")
	}
	// The kernel persisted the batch; Arc must neither report rejection nor retry.
	if len(f.guests(t, "303")) != 1 || len(f.guests(t, "ledger-303")) != 1 {
		t.Fatal("lost acknowledgement retried or rejected the batch")
	}
}

func TestValidationOnlyDecisionIsSeparateAndNeverEnrollsAgainstTheKernel(t *testing.T) {
	f := roomApp(t)
	// The filter's read runs in validation-only execution too. Enrolling there
	// would fail (no transaction exists), so success proves no enrollment.
	validated, err := f.app.Commands().Validate(f.ctx, BookRoom{ID: "404", Guest: "Ada", Compete: true})
	if !validated.IsSuccess() || err != nil || f.handled.Load() != 0 || len(f.providedReads) != 0 {
		t.Fatal(validated.Details(), err)
	}
	if len(f.filterReads[true]) != 1 || len(f.filterReads[false]) != 0 {
		t.Fatal("validation-only filter read", f.filterReads)
	}
	if got := f.guests(t, "404"); len(got) != 0 {
		t.Fatal("validation appended", got)
	}
	result, err := f.app.Commands().Execute(f.ctx, BookRoom{ID: "404", Guest: "Ada"})
	if !result.IsSuccess() || err != nil || result.Completion().Disposition != commands.Committed {
		t.Fatal(result.Details(), result.Completion(), err)
	}
	// Execution shares one issued read between the filter and Provide; the
	// validation-only read is a separate identity that was never reused.
	if len(f.filterReads[false]) != 1 || len(f.providedReads) != 1 || f.providedReads[0] != f.filterReads[false][0] || f.providedReads[0] == f.filterReads[true][0] {
		t.Fatal("decision identities", f.filterReads, f.providedReads)
	}
}

func TestDecisionProfilesAndClassifiedModelsAgainstTheKernel(t *testing.T) {
	f := roomApp(t)
	result, err := f.app.Commands().Execute(f.ctx, InspectGuestRoom{ID: "505"})
	var refused *readmodels.DecisionReadRefused
	if result.IsSuccess() || !errors.Is(err, commands.ErrDecisionRead) || !errors.As(err, &refused) || refused.Reason != readmodels.DecisionProtectedModel {
		t.Fatal("classified model was not refused", result.Details(), err)
	}
	if result.Completion().Disposition == commands.Committed {
		t.Fatal(result.Completion())
	}
	result, err = f.app.Commands().Execute(f.ctx, InspectSuite{ID: "505"})
	if result.IsSuccess() || !errors.Is(err, integration.ErrNotRegistered) {
		t.Fatal("unregistered model was read", result.Details(), err)
	}
	result, err = f.app.Commands().Execute(f.ctx, GlanceRoom{ID: "505"})
	if result.IsSuccess() || !errors.Is(err, commands.ErrDecisionProfile) {
		t.Fatal("unmarked command read a decision", result.Details(), err)
	}
	peeked, err := f.app.Commands().Execute(f.ctx, PeekRoom{ID: "505"})
	if value, _ := peeked.Response(); !peeked.IsSuccess() || err != nil || value != false {
		t.Fatal("unprotected command did not receive an advisory snapshot", peeked.Details(), value, err)
	}
	if len(f.guests(t, "505")) != 0 {
		t.Fatal("refused decisions appended")
	}
}
