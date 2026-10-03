// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	integration "github.com/cratis/arc.go/integrations/chronicle"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/eventsequences"
	"github.com/cratis/chronicle.go/seeding"
	"github.com/cratis/chronicle.go/services"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type compositionEvent struct {
	Name string `json:"name"`
}
type compositionCommand struct{}

type compositionSeeder struct {
	client *chronicle.Client
	seed   func() error
	close  func(context.Context) error
}

func (s *compositionSeeder) Seed(*seeding.Builder) error {
	if s.seed != nil {
		return s.seed()
	}
	return nil
}
func (s *compositionSeeder) Close(ctx context.Context) error {
	if s.close != nil {
		return s.close(ctx)
	}
	return nil
}

type compositionTransport struct {
	dials, unary, streams, origins atomic.Int32
	dialed                         chan struct{}
}

func (c *compositionTransport) assertIdle(t *testing.T) {
	t.Helper()
	if c.dials.Load() != 0 || c.unary.Load() != 0 || c.streams.Load() != 0 || c.origins.Load() != 0 {
		t.Fatalf("setup activated transport/origin: dial=%d unary=%d stream=%d origin=%d", c.dials.Load(), c.unary.Load(), c.streams.Load(), c.origins.Load())
	}
}

// The real grpc.ClientConn stays lazy. Unlike a fake ClientConnInterface this
// detects accidental transport activation, including RPCs outside the adapter.
func compositionConnection(t *testing.T) (*grpc.ClientConn, *compositionTransport) {
	t.Helper()
	counts := &compositionTransport{dialed: make(chan struct{}, 1)}
	conn, err := grpc.NewClient("passthrough:///composition.invalid:35000",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableRetry(),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			counts.dials.Add(1)
			select {
			case counts.dialed <- struct{}{}:
			default:
			}
			return nil, errors.New("composition witness intentionally has no kernel")
		}),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			counts.unary.Add(1)
			return invoke(ctx, method, req, reply, cc, opts...)
		}),
		grpc.WithStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, stream grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			counts.streams.Add(1)
			return stream(ctx, desc, cc, method, opts...)
		}))
	compositionRequire(t, err)
	t.Cleanup(func() { compositionRequire(t, conn.Close()) })
	return conn, counts
}

func compositionCapture(t *testing.T, conn *grpc.ClientConn, counts *compositionTransport) *chronicle.ClientPreparation {
	t.Helper()
	registry := chronicle.NewRegistry()
	_, err := chronicle.RegisterEvent[compositionEvent](registry)
	compositionRequire(t, err)
	compositionRequire(t, chronicle.RegisterSeederFactory[*compositionSeeder](registry, nil))
	preparation, err := chronicle.CaptureClient(chronicle.WithRegistry(registry), chronicle.WithGRPCConnection(conn), chronicle.WithNoAuthentication(), chronicle.WithSkipCompatibilityCheck(), chronicle.WithSkipKeepAlive(),
		chronicle.WithAppendOriginResolver(func(ctx context.Context) (eventsequences.Origin, bool, error) {
			counts.origins.Add(1)
			return sdk.ResolveAppendOrigin(ctx)
		}))
	compositionRequire(t, err)
	counts.assertIdle(t)
	return preparation
}

func compositionProvider(t *testing.T, p *chronicle.ClientPreparation, lifetime di.Lifetime, factory func(context.Context, *chronicle.Client) (*compositionSeeder, error)) di.Provider {
	t.Helper()
	var bindings container.Registry
	compositionRequire(t, di.BindValue(&bindings, p.Client()))
	compositionRequire(t, di.BindFunc1(&bindings, lifetime, factory))
	provider, err := bindings.Build()
	compositionRequire(t, err)
	// This is a frozen provider, not a late mutable resolver or a required SDK DI path.
	if err := di.BindValue(&bindings, p.Client()); !errors.Is(err, container.ErrFrozen) {
		t.Fatal("provider registry remained mutable", err)
	}
	return provider
}

func compositionApplication(t *testing.T, client *chronicle.Client, provider di.Provider) (*arc.Application, *integration.Integration) {
	t.Helper()
	adapter, err := sdk.New(client, sdk.Config{Store: "composition", OwnClient: false})
	compositionRequire(t, err)
	builder, err := arc.NewBuilder(arc.Options{ScopeFactory: provider, DependencyCatalog: provider})
	compositionRequire(t, err)
	compositionRequire(t, adapter.Install(builder))
	compositionRequire(t, commands.Register[compositionCommand](builder, commands.Handle(func(compositionCommand, context.Context) (commands.NoResponse, error) {
		return commands.NoResponse{}, nil
	})))
	app, err := builder.Build()
	compositionRequire(t, err)
	return app, adapter
}

func compositionRequire(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func compositionWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("composition fixture did not join")
		var zero T
		return zero
	}
}
func compositionRelease() (chan struct{}, func()) {
	ch := make(chan struct{})
	var once sync.Once
	return ch, func() { once.Do(func() { close(ch) }) }
}

type compositionPrepared struct {
	client *chronicle.Client
	err    error
}

func compositionPrepare(ctx context.Context, p *chronicle.ClientPreparation, provider di.Provider) (<-chan compositionPrepared, <-chan struct{}) {
	results := make(chan compositionPrepared, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client, err := services.PrepareClient(ctx, p, provider)
		results <- compositionPrepared{client, err}
	}()
	return results, done
}

func TestCapturedProviderSDKCompositionNoTransport(t *testing.T) {
	conn, counts := compositionConnection(t)
	p := compositionCapture(t, conn, counts)
	var constructed, seeded, disposed atomic.Int32
	var collaborator *compositionSeeder
	provider := compositionProvider(t, p, di.Singleton, func(_ context.Context, client *chronicle.Client) (*compositionSeeder, error) {
		if client != p.Client() {
			return nil, errors.New("captured identity changed")
		}
		constructed.Add(1)
		collaborator = &compositionSeeder{client: client, seed: func() error { seeded.Add(1); return nil }, close: func(context.Context) error { disposed.Add(1); return nil }}
		return collaborator, nil
	})
	t.Cleanup(func() {
		compositionRequire(t, p.Client().Close())
		compositionRequire(t, provider.Close(context.Background()))
	})
	counts.assertIdle(t)
	if constructed.Load() != 0 || seeded.Load() != 0 {
		t.Fatal("Capture/Build activated preparation collaborator")
	}
	client, err := services.PrepareClient(t.Context(), p, provider)
	compositionRequire(t, err)
	if client != p.Client() {
		t.Fatal("Prepare returned a different client")
	}
	repeated, err := services.PrepareClient(t.Context(), p, provider)
	compositionRequire(t, err)
	if repeated != client || constructed.Load() != 1 || seeded.Load() != 1 {
		t.Fatal("preparation did not retain its once-only identity/outcome")
	}
	scope, err := provider.NewScope(t.Context())
	compositionRequire(t, err)
	resolved, err := di.Resolve[*chronicle.Client](t.Context(), scope)
	compositionRequire(t, err)
	shared, err := di.Resolve[*compositionSeeder](t.Context(), scope)
	compositionRequire(t, err)
	if resolved != client || shared != collaborator || shared.client != client {
		t.Fatal("provider lost captured client/collaborator identity")
	}
	compositionRequire(t, scope.Close(t.Context()))
	app, adapter := compositionApplication(t, client, provider)
	compositionRequire(t, app.Start(t.Context()))
	counts.assertIdle(t)
	compositionRequire(t, app.Shutdown(t.Context()))
	compositionRequire(t, adapter.Close())
	compositionRequire(t, client.Close())
	if disposed.Load() != 0 {
		t.Fatal("borrowed provider resource disposed by Arc/client")
	}
	compositionRequire(t, provider.Close(t.Context()))
	if disposed.Load() != 1 {
		t.Fatal("provider singleton not disposed exactly once")
	}
	counts.assertIdle(t)
}

func TestUnpreparedSDKNewDoesNotPoisonPreparation(t *testing.T) {
	conn, counts := compositionConnection(t)
	p := compositionCapture(t, conn, counts)
	if adapter, err := sdk.New(p.Client(), sdk.Config{Store: "composition"}); adapter != nil || !errors.Is(err, chronicle.ErrNotPrepared) {
		t.Fatal("unprepared sdk.New", adapter, err)
	}
	entered := make(chan struct{})
	release, unblock := compositionRelease()
	var seeded atomic.Int32
	provider := compositionProvider(t, p, di.Singleton, func(_ context.Context, client *chronicle.Client) (*compositionSeeder, error) {
		return &compositionSeeder{client: client, seed: func() error { seeded.Add(1); close(entered); <-release; return nil }}, nil
	})
	results, done := compositionPrepare(t.Context(), p, provider)
	// Always release and join before closing the provider, including failed assertions.
	t.Cleanup(func() {
		unblock()
		compositionWait(t, done)
		compositionRequire(t, p.Client().Close())
		compositionRequire(t, provider.Close(context.Background()))
	})
	compositionWait(t, entered)
	if adapter, err := sdk.New(p.Client(), sdk.Config{Store: "composition"}); adapter != nil || !errors.Is(err, chronicle.ErrNotPrepared) {
		t.Fatal("running preparation accepted sdk.New", adapter, err)
	}
	if client, err := services.PrepareClient(t.Context(), p, provider); client != nil || !errors.Is(err, chronicle.ErrPreparationInProgress) {
		t.Fatal("second preparation was admitted", client, err)
	}
	counts.assertIdle(t)
	unblock()
	got := compositionWait(t, results)
	compositionWait(t, done)
	compositionRequire(t, got.err)
	if got.client != p.Client() || seeded.Load() != 1 {
		t.Fatal("guard probes poisoned preparation")
	}
	app, adapter := compositionApplication(t, got.client, provider)
	compositionRequire(t, app.Shutdown(t.Context()))
	compositionRequire(t, adapter.Close())
	counts.assertIdle(t)
}

func TestPreparationCallbackCloseDoesNotSelfWait(t *testing.T) {
	for _, stage := range []string{"callback", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			conn, counts := compositionConnection(t)
			p := compositionCapture(t, conn, counts)
			var seeded, cleaned atomic.Int32
			provider := compositionProvider(t, p, di.Scoped, func(_ context.Context, client *chronicle.Client) (*compositionSeeder, error) {
				return &compositionSeeder{client: client, seed: func() error {
					seeded.Add(1)
					if stage == "callback" {
						return client.Close()
					}
					return nil
				}, close: func(context.Context) error {
					cleaned.Add(1)
					if stage == "cleanup" {
						return client.Close()
					}
					return nil
				}}, nil
			})
			results, done := compositionPrepare(t.Context(), p, provider)
			t.Cleanup(func() {
				compositionWait(t, done)
				compositionRequire(t, p.Client().Close())
				compositionRequire(t, provider.Close(context.Background()))
			})
			got := compositionWait(t, results)
			compositionWait(t, done)
			if got.client != nil || !errors.Is(got.err, chronicle.ErrClosed) {
				t.Fatal("closed preparation published", got.client, got.err)
			}
			repeated, err := services.PrepareClient(t.Context(), p, provider)
			if repeated != nil || err != got.err || seeded.Load() != 1 || cleaned.Load() != 1 {
				t.Fatal("failed preparation result was not retained", err)
			}
			if adapter, err := sdk.New(p.Client(), sdk.Config{Store: "composition"}); adapter != nil || !errors.Is(err, chronicle.ErrClosed) {
				t.Fatal("closed preparation produced usable integration", adapter, err)
			}
			counts.assertIdle(t)
		})
	}
}

func TestPreparationCleanupJoinedBeforeProviderClose(t *testing.T) {
	conn, counts := compositionConnection(t)
	p := compositionCapture(t, conn, counts)
	entered, cleanupEntered := make(chan struct{}), make(chan struct{})
	release, unblock := compositionRelease()
	var cleaned atomic.Int32
	provider := compositionProvider(t, p, di.Scoped, func(ctx context.Context, client *chronicle.Client) (*compositionSeeder, error) {
		return &compositionSeeder{client: client, seed: func() error { close(entered); <-ctx.Done(); return ctx.Err() }, close: func(context.Context) error { close(cleanupEntered); <-release; cleaned.Add(1); return nil }}, nil
	})
	results, done := compositionPrepare(t.Context(), p, provider)
	t.Cleanup(func() {
		compositionRequire(t, p.Client().Close())
		unblock()
		compositionWait(t, done)
		compositionRequire(t, provider.Close(context.Background()))
	})
	compositionWait(t, entered)
	compositionRequire(t, p.Client().Close()) // Cancellation is not a preparation/cleanup join.
	compositionWait(t, cleanupEntered)
	select {
	case <-done:
		t.Fatal("preparation returned before resource cleanup")
	default:
	}
	if cleaned.Load() != 0 {
		t.Fatal("blocked cleanup claimed complete")
	}
	if client, err := services.PrepareClient(t.Context(), p, provider); client != nil || !errors.Is(err, chronicle.ErrPreparationInProgress) {
		t.Fatal("cleanup window lost running state", client, err)
	}
	// Provider admission still works: the application retains ownership while cleanup runs.
	scope, err := provider.NewScope(t.Context())
	compositionRequire(t, err)
	compositionRequire(t, scope.Close(t.Context()))
	unblock()
	got := compositionWait(t, results)
	compositionWait(t, done)
	if got.client != nil || !errors.Is(got.err, chronicle.ErrClosed) || cleaned.Load() != 1 {
		t.Fatal("closed preparation/cleanup outcome", got.client, got.err, cleaned.Load())
	}
	compositionRequire(t, provider.Close(t.Context()))
	if cleaned.Load() != 1 {
		t.Fatal("provider reran preparation cleanup")
	}
	counts.assertIdle(t)
}

func TestBorrowedClientSurvivesApplicationAdapterProviderClosure(t *testing.T) {
	conn, counts := compositionConnection(t)
	p := compositionCapture(t, conn, counts)
	var disposed atomic.Int32
	provider := compositionProvider(t, p, di.Singleton, func(_ context.Context, client *chronicle.Client) (*compositionSeeder, error) {
		return &compositionSeeder{client: client, close: func(context.Context) error { disposed.Add(1); return nil }}, nil
	})
	t.Cleanup(func() {
		compositionRequire(t, p.Client().Close())
		compositionRequire(t, provider.Close(context.Background()))
	})
	client, err := services.PrepareClient(t.Context(), p, provider)
	compositionRequire(t, err)
	app, adapter := compositionApplication(t, client, provider)
	compositionRequire(t, app.Start(t.Context()))
	compositionRequire(t, app.Shutdown(t.Context()))
	// Arc never owns the provider. It can still open a scope after shutdown.
	scope, err := provider.NewScope(t.Context())
	compositionRequire(t, err)
	compositionRequire(t, scope.Close(t.Context()))
	compositionRequire(t, adapter.Close())
	if disposed.Load() != 0 {
		t.Fatal("Arc/adapter closed borrowed provider")
	}
	compositionRequire(t, provider.Close(t.Context()))
	if disposed.Load() != 1 {
		t.Fatal("provider resource disposal count", disposed.Load())
	}
	counts.assertIdle(t)
	// Isolated ownership probe only, not a normal shutdown recipe. Catalogs remain
	// readable even on a closed SDK; an operational Connect must reach real transport.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	results := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); results <- client.Connect(ctx) }()
	t.Cleanup(func() { cancel(); compositionWait(t, done) })
	compositionWait(t, counts.dialed)
	cancel()
	err = compositionWait(t, results)
	compositionWait(t, done)
	if errors.Is(err, chronicle.ErrClosed) || err == nil || counts.dials.Load() == 0 || counts.unary.Load() == 0 {
		t.Fatal("borrowed client did not survive disposal", err)
	}
	compositionRequire(t, client.Close()) // Joins the SDK-owned supervisor after the probe.
}
