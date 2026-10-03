package arc_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
)

func buildStarted(t *testing.T, b *arc.Builder) (*arc.Application, error) {
	t.Helper()
	a, err := b.Build()
	if err != nil {
		return nil, err
	}
	if err := a.Start(t.Context()); err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return a, nil
}

type hook struct{ start, stop func(context.Context) error }

func (h hook) Start(ctx context.Context) error { return h.start(ctx) }
func (h hook) Stop(ctx context.Context) error  { return h.stop(ctx) }
func TestStartRollbackIncludesFailingHook(t *testing.T) {
	wantErr := errors.New("startup")
	order := []string{}
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if err := b.AddLifecycle(name, hook{start: func(context.Context) error {
			order = append(order, "start "+name)
			if name == "two" {
				return wantErr
			}
			return nil
		}, stop: func(context.Context) error { order = append(order, "stop "+name); return nil }}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); !errors.Is(err, wantErr) {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"start one", "start two", "stop two", "stop one"}) {
		t.Fatal(order)
	}
	if err := a.Start(t.Context()); !errors.Is(err, wantErr) {
		t.Fatal(err)
	}
}
func TestShutdownCancelsThenJoinsDirectOperations(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	stopped := false
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[builderCommand](b, commands.Void(func(_ builderCommand, ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	})); err != nil {
		t.Fatal(err)
	}
	if err := b.AddLifecycle("resource", hook{start: func(context.Context) error { return nil }, stop: func(context.Context) error { stopped = true; return nil }}); err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commands().Execute(t.Context(), builderCommand{}); !errors.Is(err, arc.ErrNotStarted) {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := a.Commands().Execute(t.Context(), builderCommand{}); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-canceled
	if stopped {
		t.Fatal("disposed before join")
	}
	close(release)
	<-done
	if err := a.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("not stopped")
	}
	if err := a.Start(t.Context()); !errors.Is(err, arc.ErrStopped) {
		t.Fatal(err)
	}
}
func TestRealListenerHEADAndGracefulShutdown(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Environment: "Development"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Start before launching Serve: readiness never relies on guessed sleeps.
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, listener) }()
	client := &http.Client{Timeout: time.Second}
	request, err := http.NewRequest("HEAD", "http://"+listener.Addr().String()+"/.cratis/users", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(body) != 0 || response.ContentLength != 2 {
		t.Fatal(response.StatusCode, len(body), response.ContentLength)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/.cratis/users", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
