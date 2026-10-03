//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/execution"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/seeding"
	"github.com/cratis/chronicle.go/services"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

type sharedCompositionCommand struct {
	ID    integration.EventSourceID
	Block bool
}
type sharedCompositionContextKey struct{}
type sharedCompositionCollaborator struct {
	client    *chronicle.Client
	seeds     *atomic.Int32
	disposed  *atomic.Bool
	joined    *atomic.Bool
	resources *atomic.Int32
	opened    *atomic.Int32
}

func (s *sharedCompositionCollaborator) Seed(*seeding.Builder) error { s.seeds.Add(1); return nil }
func (s *sharedCompositionCollaborator) Close() error {
	s.disposed.Store(true)
	if !s.joined.Load() || s.resources.Load() != s.opened.Load() {
		return errors.New("provider disposed before Arc/resource join")
	}
	return nil
}

type sharedCompositionResource struct{ closed *atomic.Int32 }

func (r *sharedCompositionResource) Close() error { r.closed.Add(1); return nil }

type sharedCompositionResolverCall struct {
	origin  eventsequences.Origin
	handled bool
	marker  any
	err     error
}
type sharedCompositionOutcome struct {
	result commands.Result[any]
	err    error
}

func sharedCompositionWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("composition fixture work did not join")
		var zero T
		return zero
	}
}

func TestSharedProviderCommandOriginAndJoinedShutdown(t *testing.T) {
	endpoint := os.Getenv("CHRONICLE_INTEGRATION_CONNECTION_STRING")
	if endpoint == "" {
		t.Fatal("CHRONICLE_INTEGRATION_CONNECTION_STRING is required with -tags=integration")
	}
	t.Log("kernel cratis/chronicle:19.29.4-development", os.Getenv("CHRONICLE_INTEGRATION_IMAGE_DIGEST"))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	registry := chronicle.NewRegistry()
	_, err := chronicle.RegisterEvent[AuthorCreated](registry)
	require(t, err)
	require(t, chronicle.RegisterSeederFactory[*sharedCompositionCollaborator](registry, nil))
	resolverCalls := make(chan sharedCompositionResolverCall, 4)
	// Configure before capture: sdk.New cannot retrofit an immutable client option.
	preparation, err := chronicle.CaptureClient(chronicle.WithRegistry(registry), chronicle.WithConnectionString(endpoint), chronicle.WithDevelopmentDefaults(),
		chronicle.WithAppendOriginResolver(func(callback context.Context) (eventsequences.Origin, bool, error) {
			origin, handled, err := sdk.ResolveAppendOrigin(callback)
			resolverCalls <- sharedCompositionResolverCall{origin, handled, callback.Value(sharedCompositionContextKey{}), err}
			return origin, handled, err
		}))
	require(t, err)
	var bindings container.Registry
	require(t, di.BindValue(&bindings, preparation.Client()))
	var seeds, resourceCloses, resourceOpens atomic.Int32
	var providerDisposed, applicationJoined atomic.Bool
	var collaborator *sharedCompositionCollaborator
	require(t, di.BindFunc1(&bindings, di.Singleton, func(_ context.Context, client *chronicle.Client) (*sharedCompositionCollaborator, error) {
		if client != preparation.Client() {
			return nil, errors.New("preparation client identity changed")
		}
		collaborator = &sharedCompositionCollaborator{client, &seeds, &providerDisposed, &applicationJoined, &resourceCloses, &resourceOpens}
		return collaborator, nil
	}))
	require(t, di.Bind(&bindings, di.Scoped, func(context.Context, di.Resolver) (*sharedCompositionResource, error) {
		resourceOpens.Add(1)
		return &sharedCompositionResource{&resourceCloses}, nil
	}))
	provider, err := bindings.Build()
	require(t, err)
	var app *arc.Application
	var adapter *integration.Integration
	var unsubscribe func()
	var commandDone <-chan struct{}
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// Cleanup follows the same ownership order on every failed assertion. There
	// are no owned observers/producers; the only command producer is joined here.
	t.Cleanup(func() {
		unblock()
		if commandDone != nil {
			sharedCompositionWait(t, commandDone)
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if app != nil {
			if err := app.Shutdown(cleanup); err != nil {
				t.Error("Arc drain", err)
				return
			}
		}
		applicationJoined.Store(true)
		if unsubscribe != nil {
			unsubscribe()
		} // All synchronous append callbacks are joined with commands.
		if adapter != nil {
			if err := adapter.Close(); err != nil {
				t.Error("borrowed adapter close", err)
			}
		}
		if err := preparation.Client().CloseContext(cleanup); err != nil {
			t.Error("client runtime close/join", err)
			return
		}
		// PrepareClient below is synchronous and already joined, including cleanup.
		if err := provider.Close(cleanup); err != nil {
			t.Error("provider disposal", err)
		}
	})
	client, err := services.PrepareClient(ctx, preparation, provider)
	require(t, err)
	if client != preparation.Client() || collaborator == nil || collaborator.client != client || seeds.Load() != 1 {
		t.Fatal("preparation did not use exact shared identities")
	}
	// A unique store prevents collision with other task-owned kernel consumers.
	id, err := concepts.NewUUID()
	require(t, err)
	storeName := chronicle.StoreName("arc-go29-" + id.String())
	adapter, err = sdk.New(client, sdk.Config{Store: storeName, OwnClient: false})
	require(t, err)
	builder, err := arc.NewBuilder(arc.Options{ScopeFactory: provider, DependencyCatalog: provider})
	require(t, err)
	require(t, adapter.Install(builder))
	cause := errors.New("intentional failure after confirmed immediate persistence")
	entered := make(chan struct{})
	var expectedOrigin eventsequences.Origin
	require(t, commands.Register[sharedCompositionCommand](builder, commands.Invoke(func(callback context.Context, invocation *commands.Invocation, command sharedCompositionCommand) (commands.NoResponse, error) {
		resolvedClient, err := execution.Resolve[*chronicle.Client](callback, invocation.Scope())
		if err != nil {
			return commands.NoResponse{}, err
		}
		resolvedCollaborator, err := execution.Resolve[*sharedCompositionCollaborator](callback, invocation.Scope())
		if err != nil {
			return commands.NoResponse{}, err
		}
		if resolvedClient != client || resolvedCollaborator != collaborator || resolvedCollaborator.client != client {
			return commands.NoResponse{}, errors.New("Arc did not borrow preparation provider/client/collaborator")
		}
		if command.Block {
			resource, err := execution.Resolve[*sharedCompositionResource](callback, invocation.Scope())
			if err != nil {
				return commands.NoResponse{}, err
			}
			if resource.closed.Load() != 0 {
				return commands.NoResponse{}, errors.New("resource closed before handler")
			}
			close(entered)
			<-release // Deliberately ignores cancellation to prove that cancel is not join.
			return commands.NoResponse{}, nil
		}
		expectedOrigin, _, err = sdk.ResolveAppendOrigin(callback)
		if err != nil {
			return commands.NoResponse{}, err
		}
		store, err := resolvedClient.EventStore(callback, storeName)
		if err != nil {
			return commands.NoResponse{}, err
		}
		appendResult, err := store.EventLog().Append(callback, "immediate", AuthorCreated{Name: "shared-provider"})
		if err != nil {
			return commands.NoResponse{}, err
		}
		if err := appendResult.Err(); err != nil {
			return commands.NoResponse{}, err
		}
		return commands.NoResponse{}, cause
	}), commands.WithHandlingDependencies[sharedCompositionCommand](di.KeyFor[*chronicle.Client](), di.KeyFor[*sharedCompositionCollaborator](), di.KeyFor[*sharedCompositionResource]())))
	app, err = builder.Build()
	require(t, err)
	require(t, app.Start(ctx))
	select {
	case call := <-resolverCalls:
		t.Fatal("preparation/Build/Start invoked append resolver", call)
	default:
	}
	// Preparation is not Connect, store registration, observer attachment or catch-up.
	require(t, client.Connect(ctx))
	store, err := client.EventStore(ctx, storeName)
	require(t, err)
	notifications := make(chan eventsequences.AppendNotification, 2)
	unsubscribe = store.EventLog().OnAppend(func(notification eventsequences.AppendNotification) { notifications <- notification })
	inherited := eventsequences.NewOrigin()
	commandContext := eventsequences.WithOrigin(context.WithValue(ctx, sharedCompositionContextKey{}, "runtime-marker"), inherited)
	result, err := app.Commands().Execute(commandContext, sharedCompositionCommand{ID: "root"})
	if result.IsSuccess() || !errors.Is(err, cause) || result.Completion().Disposition != commands.Committed {
		t.Fatal("immediate commitment lost after handler failure", result.Details(), result.Completion(), err)
	}
	notification := sharedCompositionWait(t, notifications)
	call := sharedCompositionWait(t, resolverCalls)
	if expectedOrigin == (eventsequences.Origin{}) || expectedOrigin == inherited || notification.Origin != expectedOrigin || call.origin != expectedOrigin || !call.handled || call.err != nil || call.marker != "runtime-marker" {
		t.Fatal("captured resolver lost runtime context or exact command attribution", expectedOrigin, inherited, notification.Origin, call)
	}
	if notification.Err != nil || notification.Result.Disposition != eventsequences.Committed || len(notification.Events) != 1 || notification.Events[0].Position == nil {
		t.Fatal("notification did not confirm persistence", notification)
	}
	persisted, err := store.EventLog().ReadSource(ctx, "immediate", eventsequences.SourceFilter{})
	require(t, err)
	if len(persisted) != 1 {
		t.Fatal("failed command's immediate event missing from kernel", persisted)
	}
	var readback AuthorCreated
	require(t, json.Unmarshal(persisted[0].Content, &readback))
	if readback.Name != "shared-provider" || persisted[0].Context.SourceID != "immediate" {
		t.Fatal("kernel readback differs from immediate append", readback, persisted[0].Context)
	}
	select {
	case extra := <-resolverCalls:
		t.Fatal("unexpected additional resolver call", extra)
	default:
	}
	unsubscribe()
	unsubscribe = nil

	outcomes := make(chan sharedCompositionOutcome, 1)
	done := make(chan struct{})
	commandDone = done
	go func() {
		defer close(done)
		result, err := app.Commands().Execute(ctx, sharedCompositionCommand{ID: "blocked", Block: true})
		outcomes <- sharedCompositionOutcome{result, err}
	}()
	sharedCompositionWait(t, entered)
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	err = app.Shutdown(short)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("short drain claimed completion", err)
	}
	if providerDisposed.Load() || resourceCloses.Load() != 0 || applicationJoined.Load() {
		t.Fatal("timeout disposed live provider resources")
	}
	select {
	case <-done:
		t.Fatal("cancellation falsely joined blocked command")
	default:
	}
	// Retain provider ownership after incomplete shutdown, then release/join work.
	scope, err := provider.NewScope(ctx)
	require(t, err)
	require(t, scope.Close(ctx))
	unblock()
	sharedCompositionWait(t, done)
	outcome := sharedCompositionWait(t, outcomes)
	if outcome.result.IsSuccess() || !errors.Is(outcome.err, context.Canceled) {
		t.Fatal("canceled admitted command result", outcome.result.Details(), outcome.err)
	}
	require(t, app.Shutdown(ctx))
	applicationJoined.Store(true)
	if resourceCloses.Load() != 1 || providerDisposed.Load() {
		t.Fatal("Arc join/resource/provider order", resourceCloses.Load(), providerDisposed.Load())
	}
	require(t, adapter.Close())
	require(t, client.CloseContext(ctx))
	require(t, provider.Close(ctx))
	if !providerDisposed.Load() {
		t.Fatal("provider did not dispose its singleton after joins")
	}
}
