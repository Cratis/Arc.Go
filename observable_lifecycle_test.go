// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

type appObservableSource func(context.Context) (observable.Stream[builderModel], error)

func (f appObservableSource) Open(ctx context.Context) (observable.Stream[builderModel], error) {
	return f(ctx)
}

type appObservableStream struct{ close func(context.Context) error }

func (appObservableStream) Next(context.Context) (builderModel, error) { return builderModel{}, io.EOF }
func (s appObservableStream) Close(ctx context.Context) error          { return s.close(ctx) }

type observableResources struct{ closes int }

func (r *observableResources) Close(context.Context) error { r.closes++; return nil }

func registerAppObservation(t *testing.T, b *arc.Builder, source observable.Source[builderModel], extra ...queries.Option[queries.NoArguments]) {
	t.Helper()
	options := []queries.Option[queries.NoArguments]{queries.WithPath[queries.NoArguments]("/observe"), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})}
	options = append(options, extra...)
	if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[builderModel], error) {
		return source, nil
	}), options...); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationObservationCapabilityAdmitsWholeLifetime(t *testing.T) {
	state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registerAppObservation(t, b, state)
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := a.Queries().(queries.ObservablePipeline)
	if !ok {
		t.Fatal("admission wrapper erased observable capability")
	}
	if _, _, err := p.Open(context.Background(), "builderModel.Observe", queries.Request{}); !errors.Is(err, arc.ErrNotStarted) {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	o, _, err := p.Open(context.Background(), "builderModel.Observe", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Open(context.Background(), "builderModel.Observe", queries.Request{}); !errors.Is(err, arc.ErrStopped) {
		t.Fatal(err)
	}
}

func TestApplicationShutdownRetainsFailedAdmissionCleanupUntilJoined(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		joined := make(chan struct{})
		holder := &observableResources{}
		b, err := arc.NewBuilder(arc.Options{Observable: arc.ObservableOptions{CloseGrace: time.Second}, OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
		if err != nil {
			t.Fatal(err)
		}
		source := appObservableSource(func(context.Context) (observable.Stream[builderModel], error) {
			cancel()
			return appObservableStream{close: func(ctx context.Context) error {
				select {
				case <-joined:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}, nil
		})
		registerAppObservation(t, b, source)
		stops := 0
		if err := b.AddLifecycle("borrowed feed", hook{start: func(context.Context) error { return nil }, stop: func(context.Context) error { stops++; return nil }}); err != nil {
			t.Fatal(err)
		}
		a, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		p := a.Queries().(queries.ObservablePipeline)
		o, _, err := p.Open(ctx, "builderModel.Observe", queries.Request{})
		if o != nil || !errors.Is(err, context.DeadlineExceeded) || holder.closes != 0 {
			t.Fatalf("failed admission %v %v closes %d", o, err, holder.closes)
		}
		budget, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := a.Shutdown(budget); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if stops != 0 || holder.closes != 0 {
			t.Fatal("resources disposed beneath unjoined failed opening")
		}
		if _, _, err := p.Open(context.Background(), "builderModel.Observe", queries.Request{}); !errors.Is(err, arc.ErrShuttingDown) {
			t.Fatal(err)
		}
		close(joined)
		if err := a.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if stops != 1 || holder.closes != 1 {
			t.Fatalf("joined: stops %d closes %d", stops, holder.closes)
		}
	})
}

func TestApplicationShutdownCancelsPendingObservationBeforeUnaryDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := arc.NewBuilder(arc.Options{})
		if err != nil {
			t.Fatal(err)
		}
		registerAppObservation(t, b, state)
		a, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		p := a.Queries().(queries.ObservablePipeline)
		o, _, err := p.Open(context.Background(), "builderModel.Observe", queries.Request{})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			done <- o.Run(context.Background(), queries.ObservationOptions{TransferMode: queries.Full}, func(queries.Result[any]) error { t.Error("pending source emitted"); return nil })
		}()
		synctest.Wait()
		if err := a.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestObservableHTTPPendingWaitAndHEADDoNotActivateSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewPendingState(observable.SubjectOptions[builderModel]{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := arc.NewBuilder(arc.Options{Observable: arc.ObservableOptions{MaximumWait: 125 * time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		if err := queries.RegisterObservable[builderModel](b, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[builderModel], error) {
			calls++
			return state, nil
		}), queries.WithPath[queries.NoArguments]("/observe")); err != nil {
			t.Fatal(err)
		}
		a, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			method, path, body string
			status             int
			text               string
		}{
			{"GET", "/observe", "", 202, `"isReady":false`},
			{"GET", "/observe?waitForFirstResult=true&waitForFirstResultTimeout=900", "", 408, "Timed out waiting 0.125 seconds"},
			{"QUERY", "/observe?waitForFirstResult=true", `{}`, 408, "Timed out waiting 0.125 seconds"},
			{"HEAD", "/observe?waitForFirstResult=true", "", 202, ""},
		} {
			before := calls
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Accept", "text/event-stream")
			a.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.text) {
				t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
			}
			if tc.method == "QUERY" && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Header())
			}
			if tc.method == "HEAD" && (calls != before || w.Body.Len() != 0) {
				t.Fatal("HEAD activated source or emitted body")
			}
		}
		if err := a.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestObservableHTTPEnumerableMessageAndHEAD(t *testing.T) {
	calls := 0
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registerAppObservation(t, b, appObservableSource(func(context.Context) (observable.Stream[builderModel], error) { calls++; return nil, nil }), queries.WithEnumerable[queries.NoArguments]())
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/observe", nil))
	if w.Code != 400 || strings.TrimSpace(w.Body.String()) != `{"message":"AsyncEnumerable queries require WebSocket connection"}` || calls != 0 {
		t.Fatalf("%d %s calls %d", w.Code, w.Body.String(), calls)
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("HEAD", "/observe?waitForFirstResult=true", nil))
	if w.Code != 202 || w.Body.Len() != 0 || calls != 0 {
		t.Fatalf("HEAD %d %s calls %d", w.Code, w.Body.String(), calls)
	}
}

func TestObservableOptionsRejectNegativeLimits(t *testing.T) {
	for _, options := range []arc.ObservableOptions{{MaxObservations: -1}, {MaximumWait: -time.Second}, {CloseGrace: -time.Second}} {
		if _, err := arc.NewBuilder(arc.Options{Observable: options}); !errors.Is(err, arc.ErrInvalidOptions) {
			t.Fatal(err)
		}
	}
}
