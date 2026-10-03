// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
)

type sourceFunc[T any] func(context.Context) (observable.Stream[T], error)

func (f sourceFunc[T]) Open(ctx context.Context) (observable.Stream[T], error) { return f(ctx) }

type streamFuncs[T any] struct {
	next  func(context.Context) (T, error)
	close func(context.Context) error
}

func (s streamFuncs[T]) Next(ctx context.Context) (T, error) { return s.next(ctx) }
func (s streamFuncs[T]) Close(ctx context.Context) error     { return s.close(ctx) }

func observableRegistry[T any](t *testing.T, source observable.Source[T], options ...queries.Option[queries.NoArguments]) *queries.Registry {
	t.Helper()
	var r queries.Registry
	options = append(options, public[queries.NoArguments]())
	mustRegister(t, queries.RegisterObservable[Item](&r, "Observe", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[T], error) { return source, nil }), options...))
	return &r
}
func observationPipeline(t *testing.T, r *queries.Registry, options queries.PipelineOptions) queries.ObservablePipeline {
	t.Helper()
	return build(t, r, options).(queries.ObservablePipeline)
}

func TestObservableRegistrationAndPendingCurrentSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending bool
		value   *Item
		ready   bool
	}{
		{name: "pending", pending: true},
		{name: "current null", ready: true},
		{name: "current zero", value: &Item{}, ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state *observable.State[*Item]
			var err error
			if tc.pending {
				state, err = observable.NewPendingState(observable.SubjectOptions[*Item]{})
			} else {
				state, err = observable.NewState(tc.value, observable.SubjectOptions[*Item]{})
			}
			mustRegister(t, err)
			p := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{})
			registration, _ := p.Lookup("Item.Observe")
			if !registration.Descriptor().Observable || registration.ReturnType() != reflect.TypeFor[observable.Source[*Item]]() || registration.DataType() != reflect.TypeFor[*Item]() {
				t.Fatal("source/emission metadata conflated")
			}
			result, err := p.Perform(context.Background(), "Item.Observe", queries.Request{})
			if err != nil || result.IsReady() != tc.ready || !result.IsAuthorized() || result.IsSuccess() != tc.ready {
				t.Fatalf("result = %+v, %v", result.Details(), err)
			}
			value, present := result.Data()
			if present != tc.ready {
				t.Fatalf("presence = %v", present)
			}
			if tc.value != nil && !reflect.DeepEqual(value, tc.value) {
				t.Fatalf("data = %#v", value)
			}
			mustRegister(t, p.CloseObservations(context.Background()))
		})
	}
}

func TestObservableDenialDoesNotActivateFactoryOrSource(t *testing.T) {
	var r queries.Registry
	calls := 0
	mustRegister(t, queries.RegisterObservable[Item](&r, "Protected", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[Item], error) { calls++; return nil, nil }), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{})))
	p := observationPipeline(t, &r, queries.PipelineOptions{})
	o, result, err := p.Open(context.Background(), "Item.Protected", queries.Request{})
	if o != nil || result.IsAuthorized() || err != nil || calls != 0 {
		t.Fatalf("admission = %v, %+v, %v; calls %d", o, result.Details(), err, calls)
	}
}

func TestObservableWaitSuppressesUntilAllowedAndDoesNotRerunFilters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewState(Item{Name: "first"}, observable.SubjectOptions[Item]{})
		mustRegister(t, err)
		r := observableRegistry(t, state)
		filters, guards := 0, 0
		mustRegister(t, r.AddFilter("once", func(context.Context, *execution.Scope) (queries.Filter, error) {
			return queries.FilterFunc(func(context.Context, *queries.Invocation) (queries.Result[any], error) {
				filters++
				return continueFilter(), nil
			}), nil
		}))
		mustRegister(t, r.AddEmissionGuard("suppress first", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
			return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
				guards++
				if !c.FirstDelivered() {
					t.Error("wait advanced delivery before success")
				}
				if guards == 1 {
					return queries.Suppress, nil
				}
				return queries.Allow, nil
			}), nil
		}))
		p := observationPipeline(t, r, queries.PipelineOptions{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			result, err := p.Perform(context.Background(), "Item.Observe", queries.Request{}.WithWait(queries.WaitOptions{ForFirstResult: true}))
			value, present := result.Data()
			if err != nil || !present || value.(Item).Name != "next" {
				t.Errorf("wait = %#v, %v", value, err)
			}
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("suppression satisfied wait")
		default:
		}
		mustRegister(t, state.Publish(context.Background(), Item{Name: "next"}))
		<-done
		if filters != 1 || guards != 2 {
			t.Fatalf("filters %d guards %d", filters, guards)
		}
	})
}

func TestObservableWaitTimeoutAndCompletionApprovedDiagnostics(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "completion"}[complete], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				state, err := observable.NewPendingState(observable.SubjectOptions[Item]{})
				mustRegister(t, err)
				if complete {
					mustRegister(t, state.Complete())
				}
				p := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{MaximumWait: 250 * time.Millisecond})
				result, err := p.Perform(context.Background(), "Item.Observe", queries.Request{}.WithWait(queries.WaitOptions{ForFirstResult: true, Timeout: time.Hour}))
				if !result.IsReady() || !result.HasExceptions() || result.IsSuccess() {
					t.Fatal(result.Details())
				}
				want := "Timed out waiting 0.25 seconds for the first observable query result."
				if complete {
					want = queries.ErrCompletedWithoutResult.Error()
					if !errors.Is(err, queries.ErrCompletedWithoutResult) {
						t.Fatal(err)
					}
				} else {
					var timeout *queries.WaitTimeoutError
					if !errors.As(err, &timeout) || timeout.Timeout != 250*time.Millisecond {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(result.Details().ExceptionMessages, []string{want}) {
					t.Fatal(result.Details())
				}
			})
		})
	}
}

func TestWaitReaderGrammarAndQUERYSeparation(t *testing.T) {
	for _, text := range []string{"1", "t", "yes", "false", ""} {
		request, err := queries.ReadGET(url.Values{"WaitForFirstResult": {text}, "waitForFirstResultTimeout": {"NaN"}})
		if err != nil || request.Wait().ForFirstResult || request.Wait().Timeout != 30*time.Second {
			t.Fatalf("%s: %+v %v", text, request.Wait(), err)
		}
	}
	for _, text := range []string{"-1", "0", "Infinity", "bad"} {
		r, err := queries.ReadGET(url.Values{"waitForFirstResultTimeout": {text}})
		if err != nil || r.Wait().Timeout != 30*time.Second {
			t.Fatalf("%s: %+v %v", text, r.Wait(), err)
		}
	}
	r, err := queries.ReadGET(url.Values{"WAITFORFIRSTRESULT": {" True "}, "waitForFirstResultTimeout": {"0.125"}})
	if err != nil || !r.Wait().ForFirstResult || r.Wait().Timeout != 125*time.Millisecond {
		t.Fatalf("wait = %+v %v", r.Wait(), err)
	}
	r, err = (queries.BodyRequestReader{}).Read(context.Background(), queries.ReaderInput{Query: url.Values{"waitForFirstResult": {"true"}}, Body: []byte(`{"arguments":{"waitForFirstResult":"application argument"}}`)})
	if err != nil || !r.Wait().ForFirstResult {
		t.Fatal(r.Wait(), err)
	}
	_, err = (queries.BodyRequestReader{}).Read(context.Background(), queries.ReaderInput{Query: url.Values{"waitForFirstResult": {"true"}, "WAITFORFIRSTRESULT": {"false"}}, Body: []byte(`{}`)})
	if err == nil {
		t.Fatal("duplicate QUERY wait controls accepted")
	}
}

func TestObservableGuardIsolationAndDenyWinsOverSuppression(t *testing.T) {
	type args struct{ Names []string }
	state, err := observable.NewState(Item{Name: "current"}, observable.SubjectOptions[Item]{})
	mustRegister(t, err)
	var r queries.Registry
	mustRegister(t, queries.RegisterObservable[Item](&r, "Observe", queries.Function(func(context.Context, args) (observable.Source[Item], error) { return state, nil }), public[args]()))
	mustRegister(t, r.AddFilter("scope", func(context.Context, *execution.Scope) (queries.Filter, error) {
		return queries.FilterFunc(func(ctx context.Context, i *queries.Invocation) (queries.Result[any], error) {
			return continueFilter(), i.SetSubscriptionScope(ctx, map[string]string{"tenant": "original"})
		}), nil
	}))
	mustRegister(t, r.AddEmissionGuard("mutating suppress", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
			c.Arguments().(args).Names[0] = "mutated"
			c.SubscriptionScope().(map[string]string)["tenant"] = "mutated"
			return queries.Suppress, nil
		}), nil
	}))
	calls := 0
	mustRegister(t, r.AddEmissionGuard("deny", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
			calls++
			if c.Arguments().(args).Names[0] != "original" || c.SubscriptionScope().(map[string]string)["tenant"] != "original" {
				t.Error("guard snapshot alias")
			}
			return queries.DenyAndTerminate, nil
		}), nil
	}))
	p := observationPipeline(t, &r, queries.PipelineOptions{})
	input := args{Names: []string{"original"}}
	result, err := p.Perform(context.Background(), "Item.Observe", queries.RequestFor(input, queries.Parameters{}))
	if err != nil || result.IsAuthorized() || calls != 1 || input.Names[0] != "original" {
		t.Fatalf("denial %+v %v calls %d", result.Details(), err, calls)
	}
}

func TestGuardFailuresFailClosedAndNeverLeakRawErrors(t *testing.T) {
	for _, mode := range []string{"factory", "error", "panic", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			state, err := observable.NewState(Item{}, observable.SubjectOptions[Item]{})
			mustRegister(t, err)
			r := observableRegistry(t, state)
			mustRegister(t, r.AddEmissionGuard("broken", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
				if mode == "factory" {
					return nil, errors.New("secret")
				}
				return queries.EmissionGuardFunc(func(context.Context, queries.EmissionContext) (queries.EmissionVerdict, error) {
					if mode == "error" {
						return queries.Allow, errors.New("secret")
					}
					if mode == "panic" {
						panic("secret")
					}
					return queries.EmissionVerdict(255), nil
				}), nil
			}))
			p := observationPipeline(t, r, queries.PipelineOptions{})
			result, err := p.Perform(context.Background(), "Item.Observe", queries.Request{})
			if err == nil || result.IsAuthorized() || !result.IsReady() {
				t.Fatalf("result %+v %v", result.Details(), err)
			}
			if strings.Contains(strings.Join(result.Details().ExceptionMessages, " "), "secret") {
				t.Fatal("raw guard error escaped")
			}
		})
	}
}

func TestObservableInterceptionDetachesSharedStateAndUsesSubscriberTenant(t *testing.T) {
	item := &Item{Name: "private"}
	state, err := observable.NewState(item, observable.SubjectOptions[*Item]{})
	mustRegister(t, err)
	r := observableRegistry(t, state)
	mustRegister(t, queries.RegisterReadModelInterceptor[*Item](r, "mask", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[*Item], error) {
		return queries.InterceptorFunc[*Item](func(ctx context.Context, item *Item) (*Item, error) {
			tenant, _ := tenancy.TenantFrom(ctx)
			item.Name = tenant.String()
			return item, nil
		}), nil
	}))
	p := observationPipeline(t, r, queries.PipelineOptions{})
	for _, name := range []string{"one", "two"} {
		tenant, err := tenancy.ParseID(name)
		mustRegister(t, err)
		ctx := tenancy.WithTenant(context.Background(), tenant)
		result, err := p.Perform(ctx, "Item.Observe", queries.Request{})
		data, _ := result.Data()
		if err != nil || data.(*Item).Name != name || item.Name != "private" {
			t.Fatalf("result %#v %v; shared %#v", data, err, item)
		}
	}
}

func TestRunAcknowledgesDeliveryAndDenialIsTerminal(t *testing.T) {
	source := observable.FromProducer(func(ctx context.Context, emit func(Item) error) error {
		for i := 0; i < 3; i++ {
			if err := emit(Item{ID: i}); err != nil {
				return err
			}
		}
		return nil
	})
	r := observableRegistry(t, source)
	guards := 0
	mustRegister(t, r.AddEmissionGuard("two only", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(_ context.Context, c queries.EmissionContext) (queries.EmissionVerdict, error) {
			if c.FirstDelivered() != (guards == 0) {
				t.Error("first delivery flag")
			}
			guards++
			if guards == 2 {
				return queries.DenyAndTerminate, nil
			}
			return queries.Allow, nil
		}), nil
	}))
	p := observationPipeline(t, r, queries.PipelineOptions{})
	o, _, err := p.Open(context.Background(), "Item.Observe", queries.Request{})
	mustRegister(t, err)
	frames := 0
	err = o.Run(context.Background(), queries.ObservationOptions{TransferMode: queries.Full}, func(result queries.Result[any]) error {
		frames++
		if result.IsAuthorized() != (frames == 1) {
			t.Error("terminal denial frame")
		}
		return nil
	})
	mustRegister(t, err)
	mustRegister(t, o.Close(context.Background()))
	if frames != 2 || guards != 2 {
		t.Fatalf("frames %d guards %d", frames, guards)
	}
	if !errors.Is(o.Run(context.Background(), queries.ObservationOptions{TransferMode: queries.Full}, func(queries.Result[any]) error { return nil }), queries.ErrObservationRunning) {
		t.Fatal("Run was reusable")
	}
}

func TestFailedOpeningRetainsTimedOutCleanupForLaterShutdownJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		joined := make(chan struct{})
		closeCalls := 0
		holder := &plainResources{}
		source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) {
			cancel() // Admission fails its post-callback continuity check after activation.
			return streamFuncs[Item]{next: func(context.Context) (Item, error) { return Item{}, io.EOF }, close: func(ctx context.Context) error {
				closeCalls++
				select {
				case <-joined:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}, nil
		})
		p := observationPipeline(t, observableRegistry(t, source), queries.PipelineOptions{CleanupTimeout: time.Second, MaxObservations: 1, OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
		o, result, err := p.Open(ctx, "Item.Observe", queries.Request{})
		if o != nil || !errors.Is(err, context.DeadlineExceeded) || !result.HasExceptions() || closeCalls != 1 || holder.closes != 0 {
			t.Fatalf("failed admission %v %+v %v; close %d scope %d", o, result.Details(), err, closeCalls, holder.closes)
		}
		_, _, err = p.Open(context.Background(), "Item.Observe", queries.Request{})
		if !errors.Is(err, queries.ErrObservationCapacity) {
			t.Fatalf("unjoined operation did not consume capacity: %v", err)
		}
		shutdown, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := p.CloseObservations(shutdown); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if holder.closes != 0 {
			t.Fatal("disposed resources before producer joined")
		}
		close(joined)
		mustRegister(t, p.CloseObservations(context.Background()))
		if holder.closes != 1 || closeCalls != 3 {
			t.Fatalf("later join: close %d scope %d", closeCalls, holder.closes)
		}
		_, _, err = p.Open(context.Background(), "Item.Observe", queries.Request{})
		if !errors.Is(err, queries.ErrObservationsStopping) {
			t.Fatal(err)
		}
	})
}

func TestObservationCloseJoinsBlockedDeliveryBeforeDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		state, err := observable.NewState(Item{}, observable.SubjectOptions[Item]{})
		mustRegister(t, err)
		holder := &plainResources{}
		p := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
		o, _, err := p.Open(context.Background(), "Item.Observe", queries.Request{})
		mustRegister(t, err)
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			err := o.Run(context.Background(), queries.ObservationOptions{TransferMode: queries.Full}, func(queries.Result[any]) error { close(entered); <-release; return nil })
			if !errors.Is(err, context.Canceled) {
				t.Errorf("run = %v", err)
			}
		}()
		<-entered
		budget, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := o.Close(budget); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if holder.closes != 0 {
			t.Fatal("resources disposed beneath active delivery")
		}
		close(release)
		<-done
		mustRegister(t, p.CloseObservations(context.Background()))
		if holder.closes != 1 {
			t.Fatal("resources not disposed after join")
		}
	})
}

func TestObservableCleanupFailureSuppressesUnpublishedSnapshot(t *testing.T) {
	state, err := observable.NewState(Item{Name: "not deliverable"}, observable.SubjectOptions[Item]{})
	mustRegister(t, err)
	holder := &plainResources{closeErr: errors.New("private cleanup failure")}
	p := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
	result, err := p.Perform(context.Background(), "Item.Observe", queries.Request{})
	_, present := result.Data()
	if err == nil || present || !result.HasExceptions() || strings.Contains(result.Details().ExceptionMessages[0], "private") {
		t.Fatalf("snapshot %+v %v", result.Details(), err)
	}
}

func TestObservableEnumerableSnapshotDoesNotActivateSource(t *testing.T) {
	calls := 0
	source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) { calls++; return nil, nil })
	p := observationPipeline(t, observableRegistry(t, source, queries.WithEnumerable[queries.NoArguments]()), queries.PipelineOptions{})
	_, err := p.Perform(context.Background(), "Item.Observe", queries.Request{})
	if !errors.Is(err, queries.ErrEnumerableRequiresStreaming) || calls != 0 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestSubscriptionScopeRejectsStreamsAndRetainedInvocation(t *testing.T) {
	state, err := observable.NewState(Item{}, observable.SubjectOptions[Item]{})
	mustRegister(t, err)
	r := observableRegistry(t, state)
	var retained *queries.Invocation
	mustRegister(t, r.AddFilter("scope", func(context.Context, *execution.Scope) (queries.Filter, error) {
		return queries.FilterFunc(func(ctx context.Context, i *queries.Invocation) (queries.Result[any], error) {
			retained = i
			if err := i.SetSubscriptionScope(ctx, map[string]any{"stream": state}); err == nil {
				t.Error("stream accepted as scope data")
			}
			return continueFilter(), i.SetSubscriptionScope(ctx, map[string]string{"ok": "value"})
		}), nil
	}))
	p := observationPipeline(t, r, queries.PipelineOptions{})
	o, _, err := p.Open(context.Background(), "Item.Observe", queries.Request{})
	mustRegister(t, err)
	if err := retained.SetSubscriptionScope(context.Background(), "late"); !errors.Is(err, execution.ErrScopeExpired) {
		t.Fatal(err)
	}
	mustRegister(t, o.Close(context.Background()))
}

func TestRunRejectsChangedSubscriberIdentity(t *testing.T) {
	state, err := observable.NewState(Item{}, observable.SubjectOptions[Item]{})
	mustRegister(t, err)
	p := observationPipeline(t, observableRegistry(t, state), queries.PipelineOptions{})
	o, _, err := p.Open(context.Background(), "Item.Observe", queries.Request{})
	mustRegister(t, err)
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{})
	err = o.Run(ctx, queries.ObservationOptions{TransferMode: queries.Full}, func(queries.Result[any]) error { t.Fatal("changed identity delivered"); return nil })
	if !errors.Is(err, execution.ErrIdentityChanged) {
		t.Fatal(err)
	}
	mustRegister(t, o.Close(context.Background()))
}
