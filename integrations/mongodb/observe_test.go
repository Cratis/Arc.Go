// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func watchPipeline(t *testing.T, source observable.Source[Find[author]], rendererFactory queries.Factory[queries.Renderer[Find[author], []author]], config queries.PipelineOptions, names ...string) queries.ObservablePipeline {
	t.Helper()
	var registry queries.Registry
	for _, name := range names {
		if err := queries.RegisterObservable[author, queries.NoArguments, Find[author]](&registry, name,
			queries.Function(func(context.Context, queries.NoArguments) (observable.Source[Find[author]], error) {
				return source, nil
			}),
			queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}),
			queries.WithRenderer[queries.NoArguments, Find[author], []author](rendererFactory)); err != nil {
			t.Fatal(err)
		}
	}
	pipeline, err := registry.Build(config)
	if err != nil {
		t.Fatal(err)
	}
	return pipeline.(queries.ObservablePipeline)
}

func TestObserveUsesExistingRendererForWaitAndEachStreamingEmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		cursor := newWatchTestCursor()
		w, binding := testWatcher(t, WatcherOptions{}, cursor)
		t.Cleanup(func() { testWatchClose(t, w) })
		filter := bson.D{{Key: "large", Value: int64(1)}, {Key: "small", Value: int32(1)}}
		source, err := Observe(w, binding, Find[author]{Filter: filter})
		if err != nil {
			t.Fatal(err)
		}
		fake := &recordingCollection{total: 1}
		fake.afterCount = func(context.Context) { fake.cursor = &recordingCursor{documents: []bson.Raw{rawAuthor(t, 1, "Ada")}} }
		rowCalls := 0
		renderer, err := NewRenderer(binding, RendererOptions[author]{RowFilter: func(context.Context, queries.QueryContext) (bson.D, error) { rowCalls++; return bson.D{}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		renderer.open = func(tenancy.ID) (snapshotCollection, error) { return fake, nil }
		var views []*execution.Scope
		pipeline := watchPipeline(t, source, func(ctx context.Context, view *execution.Scope) (queries.Renderer[Find[author], []author], error) {
			if err := view.CheckContext(ctx); err != nil {
				return nil, err
			}
			views = append(views, view)
			return renderer, nil
		}, queries.PipelineOptions{}, "Observe")
		request := queries.RequestFor(queries.NoArguments{}, queries.Parameters{Paging: queries.Paging{IsPaged: true, Size: 10}})
		pending, err := queries.Perform[[]author](ctx, pipeline, "author.Observe", request)
		if err != nil || pending.IsReady() || fake.counts != 0 {
			t.Fatalf("non-wait source-only snapshot must remain pending: %+v %v", pending.Details(), err)
		}
		wait, err := queries.Perform[[]author](ctx, pipeline, "author.Observe", request.WithWait(queries.WaitOptions{ForFirstResult: true}))
		data, present := wait.Data()
		if err != nil || !wait.IsReady() || !present || len(data) != 1 || data[0].Name != "Ada" || wait.Details().Paging.TotalItems != 1 {
			t.Fatalf("wait did not render actual initial baseline: %+v %v", wait.Details(), err)
		}
		observation, _, err := pipeline.Open(ctx, "author.Observe", request)
		if err != nil {
			t.Fatal(err)
		}
		delivered := make(chan struct{})
		finish := errors.New("test delivery complete")
		run := make(chan error, 1)
		go func() {
			count := 0
			run <- observation.Run(ctx, queries.ObservationOptions{}, func(result queries.Result[any]) error {
				value, ok := result.Data()
				if !ok || !reflect.DeepEqual(value, []author{{ID: 1, Name: "Ada"}}) {
					t.Errorf("synthetic or invalid baseline: %+v", result.Details())
				}
				count++
				if count == 2 {
					return finish
				}
				close(delivered)
				return nil
			})
		}()
		<-delivered
		cursor.events <- changeEvent(t, "update", "Library", "Authors")
		if !errors.Is(<-run, finish) {
			t.Fatal("stream did not rerender invalidation")
		}
		if fake.counts != 3 || fake.finds != 3 || rowCalls != 3 || len(views) != 3 {
			t.Fatalf("renderer bypass: counts=%d finds=%d rows=%d views=%d", fake.counts, fake.finds, rowCalls, len(views))
		}
		for i, view := range views {
			if !errors.Is(view.CheckContext(ctx), execution.ErrScopeExpired) {
				t.Fatal("callback scope remains usable after emission")
			}
			for j := 0; j < i; j++ {
				if views[j] == view {
					t.Fatal("captured renderer scope reused")
				}
			}
		}
		selection := fake.countFilter.Lookup("$and").Array().Index(1).Document()
		if selection.Lookup("large").Type != bson.TypeInt64 || selection.Lookup("small").Type != bson.TypeInt32 {
			t.Fatal("Arc emission detachment changed BSON widths")
		}
		testWatchClose(t, observation)
		if err := pipeline.CloseObservations(ctx); err != nil {
			t.Fatal(err)
		}
		testWatchClose(t, w)
	})
}

func TestObserveResolvesTenantsAndLogicalQueryLimitsAtOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		w, binding := testWatcher(t, WatcherOptions{MaxSubscribersPerQuery: 1}, newWatchTestCursor())
		var opened []string
		w.open = func(_ context.Context, database string) (snapshotCursor, error) {
			opened = append(opened, database)
			return newWatchTestCursor(), nil
		}
		source := testObserve(t, w, binding)
		factory := func(context.Context, *execution.Scope) (queries.Renderer[Find[author], []author], error) {
			return nil, errors.New("no renderer execution expected")
		}
		pipeline := watchPipeline(t, source, factory, queries.PipelineOptions{Membership: tenancy.MembershipFunc(func(context.Context, identity.Principal, tenancy.ID) (bool, error) { return true, nil })}, "One", "Two")
		a, _, err := pipeline.Open(ctx, "author.One", queries.Request{})
		if err != nil {
			t.Fatal(err)
		}
		if duplicate, _, err := pipeline.Open(ctx, "author.One", queries.Request{}); duplicate != nil || !errors.Is(err, ErrLimit) {
			t.Fatalf("logical query limit bypass: %v", err)
		}
		b, _, err := pipeline.Open(ctx, "author.Two", queries.Request{})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Blue", "Green"} {
			tenant, err := tenancy.ParseID(name)
			if err != nil {
				t.Fatal(err)
			}
			o, _, err := pipeline.Open(tenancy.WithTenant(ctx, tenant), "author.One", queries.Request{})
			if err != nil {
				t.Fatal(err)
			}
			testWatchClose(t, o)
		}
		if !reflect.DeepEqual(opened, []string{"Library", "Library+Blue", "Library+Green"}) {
			t.Fatalf("resolved watch identity: %v", opened)
		}
		testWatchClose(t, a)
		testWatchClose(t, b)
		if err := pipeline.CloseObservations(ctx); err != nil {
			t.Fatal(err)
		}
		testWatchClose(t, w)
		// A separate client/owner is never pooled with the first one.
		other, err := NewWatcher(ctx, &mongo.Client{}, WatcherOptions{})
		if err != nil {
			t.Fatal(err)
		}
		other.open = func(context.Context, string) (snapshotCursor, error) { return newWatchTestCursor(), nil }
		stream := testWatchOpen(t, testObserve(t, other, testWatchBinding[author](t, other.client, "Library", "Authors")))
		testWatchNext(t, stream)
		testWatchClose(t, stream)
		testWatchClose(t, other)
	})
}
