// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	consumer "example.test/consumer"
	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

type feed struct {
	trace *[]string
}

func (f feed) ForBoard(context.Context, string) (observable.Source[[]consumer.Task], error) {
	*f.trace = append(*f.trace, "factory")
	return source{trace: f.trace}, nil
}

type source struct{ trace *[]string }

func (s source) Open(context.Context) (observable.Stream[[]consumer.Task], error) {
	*s.trace = append(*s.trace, "open")
	return &stream{trace: s.trace}, nil
}

type stream struct {
	trace   *[]string
	pending bool
	closed  bool
}

func (s *stream) Next(context.Context) ([]consumer.Task, error) { return nil, io.EOF }
func (s *stream) Close(context.Context) error {
	if !s.pending {
		s.pending = true
		*s.trace = append(*s.trace, "join-pending")
		return observable.ErrJoinPending
	}
	if !s.closed {
		s.closed = true
		*s.trace = append(*s.trace, "joined")
	}
	return nil
}

type resources struct{ trace *[]string }

func (r resources) Close(context.Context) error { *r.trace = append(*r.trace, "dispose"); return nil }

func TestManualAndGeneratedObservableContracts(t *testing.T) {
	var traces [][]string
	var declarations []metadata.Query
	for _, generated := range []bool{false, true} {
		trace := []string{}
		builder, err := arc.NewBuilder(arc.Options{OpenResources: func(context.Context) (execution.Resources, error) { return resources{&trace}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		resolve := func(context.Context, *execution.Scope) (consumer.TaskFeed, error) {
			trace = append(trace, "resolve")
			return feed{trace: &trace}, nil
		}
		if generated {
			err = consumer.RegisterArtifacts(builder, consumer.ArcBindings{ResolveTaskFeed: resolve})
		} else {
			err = queries.RegisterReadModel[consumer.Task](builder, queries.WithModelNamespace("Shop.Tasks"), queries.WithModelAuthorization(metadata.Authorization{AllowAnonymous: true}))
			if err == nil {
				err = queries.RegisterObservable[consumer.Task, consumer.WatchArgs, []consumer.Task](builder, "Watch", queries.Invoke(func(ctx context.Context, inv *queries.Invocation, args consumer.WatchArgs) (observable.Source[[]consumer.Task], error) {
					f, err := resolve(ctx, inv.Scope())
					if err != nil {
						return nil, err
					}
					return (consumer.Task{}).Watch(ctx, args, f)
				}))
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		app, err := builder.Build()
		if err != nil {
			t.Fatal(err)
		}
		if len(trace) != 0 {
			t.Fatalf("Build activated source: %v", trace)
		}
		if err := app.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		p := app.Queries().(queries.ObservablePipeline)
		registration, ok := p.Lookup("Shop.Tasks.Task.Watch")
		if !ok {
			t.Fatal("missing registration")
		}
		declarations = append(declarations, registration.Descriptor())
		if registration.ReturnType() != reflect.TypeFor[observable.Source[[]consumer.Task]]() || registration.EmissionType() != reflect.TypeFor[[]consumer.Task]() || registration.DataType() != reflect.TypeFor[[]consumer.Task]() || registration.ArgumentType() != reflect.TypeFor[consumer.WatchArgs]() || len(registration.Parameters()) != 1 {
			t.Fatal("normalized registration types drifted")
		}
		_, result, err := p.Open(t.Context(), "Shop.Tasks.Task.Watch", queries.Request{})
		if err == nil || result.IsSuccess() {
			t.Fatalf("invalid input admitted: %v", err)
		}
		if len(trace) != 0 {
			t.Fatalf("invalid admission activation: %v", trace)
		}
		trace = nil
		if generated {
			_, result, err = p.Open(t.Context(), "Shop.Tasks.Task.Private", queries.RequestFor(consumer.WatchArgs{Board: "a"}, queries.Parameters{}))
			if err != nil || result.IsAuthorized() {
				t.Fatalf("private admitted: %v", err)
			}
			if !reflect.DeepEqual(trace, []string{"dispose"}) {
				t.Fatalf("denied activation: %v", trace)
			}
			trace = nil
		}
		o, result, err := p.Open(t.Context(), "Shop.Tasks.Task.Watch", queries.RequestFor(consumer.WatchArgs{Board: "a"}, queries.Parameters{}))
		if err != nil || !result.IsSuccess() {
			t.Fatalf("open: %v %+v", err, result.Details())
		}
		if err := o.Close(t.Context()); !errors.Is(err, observable.ErrJoinPending) {
			t.Fatalf("first join: %v", err)
		}
		if !reflect.DeepEqual(trace, []string{"resolve", "factory", "open", "join-pending"}) {
			t.Fatalf("disposed before join: %v", trace)
		}
		if err := o.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := o.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		traces = append(traces, append([]string(nil), trace...))
		if generated {
			for _, tc := range []struct {
				name  string
				ready bool
				data  any
			}{
				{"Single", true, consumer.Task{}}, {"Current", true, consumer.Task{}}, {"Nullable", true, nil}, {"Pending", false, nil}, {"Subject", false, nil},
			} {
				result, err := p.Perform(t.Context(), queries.FullyQualifiedQueryName("Shop.Tasks.Task."+tc.name), queries.Request{})
				data, present := result.Data()
				if err != nil || result.IsReady() != tc.ready || present != tc.ready || !reflect.DeepEqual(data, tc.data) {
					t.Fatalf("snapshot %s: ready=%v present=%v data=%#v err=%v", tc.name, result.IsReady(), present, data, err)
				}
			}
		}
		if err := app.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(traces[0], traces[1]) || !reflect.DeepEqual(traces[0], []string{"resolve", "factory", "open", "join-pending", "joined", "dispose"}) {
		t.Fatalf("lifecycle mismatch: %v", traces)
	}
	if !reflect.DeepEqual(declarations[0], declarations[1]) {
		t.Fatalf("descriptor mismatch: %v", declarations)
	}
}

type lifecycleFeed struct {
	trace   *[]string
	mode    string
	failure error
}

func (f lifecycleFeed) ForBoard(context.Context, string) (observable.Source[[]consumer.Task], error) {
	*f.trace = append(*f.trace, "factory")
	if f.mode == "current" {
		return observable.NewState([]consumer.Task{}, observable.SubjectOptions[[]consumer.Task]{})
	}
	return lifecycleSource{f}, nil
}

type lifecycleSource struct{ lifecycleFeed }

func (s lifecycleSource) Open(context.Context) (observable.Stream[[]consumer.Task], error) {
	*s.trace = append(*s.trace, "open")
	stream := lifecycleStream{s.lifecycleFeed}
	if s.mode == "startup" {
		return stream, s.failure
	}
	return stream, nil
}

type lifecycleStream struct{ lifecycleFeed }

func (s lifecycleStream) Next(ctx context.Context) ([]consumer.Task, error) {
	if s.mode == "cancel" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.mode == "failure" {
		return nil, s.failure
	}
	return nil, io.EOF
}
func (s lifecycleStream) Close(context.Context) error {
	*s.trace = append(*s.trace, "joined")
	return nil
}

func TestGeneratedSourceLifecycleTerminalAndCollectionSnapshots(t *testing.T) {
	for _, mode := range []string{"eof", "failure", "startup", "cancel", "current"} {
		t.Run(mode, func(t *testing.T) {
			trace := []string{}
			failure := errors.New("source failure")
			builder, err := arc.NewBuilder(arc.Options{OpenResources: func(context.Context) (execution.Resources, error) { return resources{&trace}, nil }})
			if err != nil {
				t.Fatal(err)
			}
			if err := consumer.RegisterArtifacts(builder, consumer.ArcBindings{ResolveTaskFeed: func(context.Context, *execution.Scope) (consumer.TaskFeed, error) {
				trace = append(trace, "resolve")
				return lifecycleFeed{&trace, mode, failure}, nil
			}}); err != nil {
				t.Fatal(err)
			}
			app, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			if len(trace) != 0 {
				t.Fatal("eager factory")
			}
			if err := app.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := app.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			p := app.Queries().(queries.ObservablePipeline)
			request := queries.RequestFor(consumer.WatchArgs{Board: "a"}, queries.Parameters{})
			if mode == "current" {
				result, err := p.Perform(t.Context(), "Shop.Tasks.Task.Watch", request)
				data, present := result.Data()
				if err != nil || !result.IsReady() || !present || !reflect.DeepEqual(data, []consumer.Task{}) {
					t.Fatalf("collection snapshot: %#v %v", data, err)
				}
				if !reflect.DeepEqual(trace, []string{"resolve", "factory", "dispose"}) {
					t.Fatalf("snapshot unexpectedly opened a stream: %v", trace)
				}
				return
			}
			o, _, err := p.Open(t.Context(), "Shop.Tasks.Task.Watch", request)
			if mode == "startup" {
				if o != nil || !errors.Is(err, failure) || !reflect.DeepEqual(trace, []string{"resolve", "factory", "open", "joined", "dispose"}) {
					t.Fatalf("startup ownership: %v %v", trace, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			callbacks := 0
			err = o.Run(ctx, queries.ObservationOptions{TransferMode: queries.Full}, func(result queries.Result[any]) error {
				callbacks++
				if !result.HasExceptions() {
					t.Error("failure was not terminal")
				}
				return nil
			})
			switch mode {
			case "eof":
				if err != nil || callbacks != 0 {
					t.Fatalf("EOF: %v %d", err, callbacks)
				}
			case "failure":
				if !errors.Is(err, failure) || callbacks != 1 {
					t.Fatalf("failure: %v %d", err, callbacks)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || callbacks != 0 {
					t.Fatalf("cancellation: %v %d", err, callbacks)
				}
			}
			if err := o.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(trace, []string{"resolve", "factory", "open", "joined", "dispose"}) {
				t.Fatalf("terminal join order: %v", trace)
			}
		})
	}
}
