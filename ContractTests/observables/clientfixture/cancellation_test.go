// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package clientfixture_test

import (
	"context"
	"errors"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

// gatedCancellation holds Run's cancellation forwarding until the test releases
// it. The consumer/source can observe cancellation before Run's child does,
// reproducing the ancestor-to-child propagation window without scheduler timing.
type gatedCancellation struct {
	context.Context
	registered chan struct{}
	release    chan struct{}
	joined     chan struct{}
}

// Value hides the underlying cancel-context optimization so context uses the
// explicit AfterFunc hook. This test context carries no request metadata.
func (*gatedCancellation) Value(any) any { return nil }

func (c *gatedCancellation) AfterFunc(call func()) func() bool {
	stop := context.AfterFunc(c.Context, func() {
		defer close(c.joined)
		<-c.release
		call()
	})
	close(c.registered)
	return stop
}

func TestCanceledConsumerBeforeRunChildDoesNotDeliverSourceFailure(t *testing.T) {
	consumer, cancel := context.WithCancel(t.Context())
	defer cancel()
	gated := &gatedCancellation{Context: consumer, registered: make(chan struct{}), release: make(chan struct{}), joined: make(chan struct{})}
	source := &cancelingSource{consumer: consumer}
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterObservable[clientfixtureItem](builder, "Pending", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[clientfixtureItem], error) {
		return source, nil
	})); err != nil {
		t.Fatal(err)
	}
	app, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	pipeline := app.Queries().(queries.ObservablePipeline)
	observation, _, err := pipeline.Open(t.Context(), "clientfixtureItem.Pending", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		err        error
		deliveries int
	}
	done := make(chan outcome, 1)
	go func() {
		var deliveries int
		err := observation.Run(gated, queries.ObservationOptions{TransferMode: queries.Full}, func(queries.Result[any]) error {
			deliveries++
			return nil
		})
		done <- outcome{err: err, deliveries: deliveries}
	}()
	select {
	case <-gated.registered:
	case <-time.After(time.Second):
		t.Fatal("Run did not install cancellation forwarding")
	}
	defer func() {
		close(gated.release)
		select {
		case <-gated.joined:
		case <-time.After(time.Second):
			t.Error("cancellation forwarding did not join")
		}
	}()
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || result.deliveries != 0 {
			t.Fatalf("canceled consumer: error=%v deliveries=%d, want cancellation without a terminal result", result.err, result.deliveries)
		}
	case <-time.After(time.Second):
		t.Fatal("Run waited for deferred child cancellation")
	}
}

type clientfixtureItem struct{}

type cancelingSource struct{ consumer context.Context }

func (s *cancelingSource) Open(context.Context) (observable.Stream[clientfixtureItem], error) {
	return s, nil
}

func (s *cancelingSource) Next(ctx context.Context) (clientfixtureItem, error) {
	select {
	case <-s.consumer.Done():
		return clientfixtureItem{}, s.consumer.Err()
	case <-ctx.Done():
		return clientfixtureItem{}, ctx.Err()
	}
}

func (*cancelingSource) Close(context.Context) error { return nil }
