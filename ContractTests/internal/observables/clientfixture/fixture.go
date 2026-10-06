// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package clientfixture composes the real Arc host used by the locked client lane.
package clientfixture

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/ContractTests/internal/observables/generatedconsumerfixture"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

// Item is shared by manual and generated registration modes.
type Item = generatedconsumerfixture.Item

// Arguments is shared by both registration modes.
type Arguments = generatedconsumerfixture.Arguments

type arguments = Arguments

// Signals counts cumulative lifecycle events, not merely the current active set.
type Signals struct {
	Resolve int `json:"resolve"`
	Factory int `json:"factory"`
	Open    int `json:"open"`
	Close   int `json:"close"`
	Dispose int `json:"dispose"`
}

// Fixture owns immutable subject values and tracks actual stream cleanup.
// Control handlers never construct successful query results or transport frames.
type Fixture struct {
	App          *arc.Application
	Names        map[string]string
	states       map[string]*observable.State[[]Item]
	deny         atomic.Bool
	mu           sync.Mutex
	active       map[string]int
	changed      chan struct{}
	signals      Signals
	shutdown     chan struct{}
	shutdownOnce sync.Once
}

// New builds without starting a listener. The caller owns App.Serve and its join.
func New() (*Fixture, error) { return NewMode(false) }

// NewMode preserves manual registration and optionally uses production-generated adapters.
func NewMode(generated bool) (*Fixture, error) { return newFixture(generated, nil) }

func newFixture(generated bool, browserAssets fs.FS) (*Fixture, error) {
	f := &Fixture{Names: map[string]string{}, states: map[string]*observable.State[[]Item]{}, active: map[string]int{}, changed: make(chan struct{}), shutdown: make(chan struct{})}
	for _, group := range []string{"alpha", "beta", "pending", "nil", "guarded"} {
		var state *observable.State[[]Item]
		var err error
		switch group {
		case "pending":
			state, err = observable.NewPendingState(observable.SubjectOptions[[]Item]{Buffer: 16})
		case "nil":
			state, err = observable.NewState[[]Item](nil, observable.SubjectOptions[[]Item]{Buffer: 16})
		default:
			items := []Item{{ID: "a", Title: group + "-old"}, {ID: "b", Title: group + "-gone"}}
			if generated {
				for i := range items {
					items[i].CreatedAt = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
					items[i].Rank = 7 - i
					items[i].Enabled = true
				}
			}
			state, err = observable.NewState(items, observable.SubjectOptions[[]Item]{Buffer: 16})
		}
		if err != nil {
			return nil, err
		}
		f.states[group] = state
	}
	transport := arc.ObservableOptions{}
	if browserAssets == nil {
		// Preserve the explicit Node session. Browser mode must use Arc cookies.
		transport.AnonymousOwner = func(context.Context, *http.Request) (string, error) { return "loopback-client-fixture", nil }
	}
	builder, err := arc.NewBuilder(arc.Options{OpenResources: func(context.Context) (execution.Resources, error) { return &feedResources{fixture: f}, nil }, HTTP: arc.HTTPOptions{ShutdownTimeout: 5 * time.Second}, Observable: transport})
	if err != nil {
		return nil, err
	}
	if generated {
		err = generatedconsumerfixture.RegisterArtifacts(builder, generatedconsumerfixture.ArcBindings{ResolveItemFeed: func(_ context.Context, scope *execution.Scope) (generatedconsumerfixture.ItemFeed, error) {
			f.signal(func(s *Signals) { s.Resolve++ })
			return scope.Resources().(*feedResources), nil
		}})
	} else {
		err = queries.RegisterObservable[Item](builder, "All", queries.Function(func(ctx context.Context, args arguments) (observable.Source[[]Item], error) {
			return (&feedResources{fixture: f}).ForGroup(ctx, args.Group)
		}), queries.WithPath[arguments]("/items"), queries.WithAuthorization[arguments](metadata.Authorization{AllowAnonymous: true}))
	}
	if err != nil {
		return nil, err
	}
	if err := builder.Queries().AddEmissionGuard("fixture-denial", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
		return queries.EmissionGuardFunc(func(context.Context, queries.EmissionContext) (queries.EmissionVerdict, error) {
			if f.deny.Load() {
				return queries.DenyAndTerminate, nil
			}
			return queries.Allow, nil
		}), nil
	}); err != nil {
		return nil, err
	}
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /fixture/ready":    f.ready,
		"POST /fixture/publish": f.publish,
		"POST /fixture/deny":    func(w http.ResponseWriter, _ *http.Request) { f.deny.Store(true); w.WriteHeader(http.StatusNoContent) },
		"GET /fixture/wait":     f.wait,
		"POST /fixture/shutdown": func(w http.ResponseWriter, _ *http.Request) {
			f.shutdownOnce.Do(func() { close(f.shutdown) })
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /fixture/signals": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.Signals())
		},
	} {
		if err := builder.Handle(pattern, handler); err != nil {
			return nil, err
		}
	}
	if browserAssets != nil {
		handler := http.StripPrefix("/fixture/browser/", http.FileServerFS(browserAssets))
		if err := builder.Handle("GET /fixture/browser/", handler); err != nil {
			return nil, err
		}
	}
	f.App, err = builder.Build()
	if err != nil {
		return nil, err
	}
	for _, query := range f.App.Catalog().Queries {
		f.Names[query.Name] = query.Identity()
	}
	return f, nil
}

func (f *Fixture) ready(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(f.Names); err != nil {
		return
	} // peer disconnect owns the failure
}
func (f *Fixture) publish(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Group string `json:"group"`
		Items []Item `json:"items"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	defer func() { _ = r.Body.Close() }()
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state, ok := f.states[input.Group]
	if !ok {
		http.Error(w, "unknown fixture group", http.StatusBadRequest)
		return
	}
	if err := state.Publish(r.Context(), input.Items); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (f *Fixture) delta(group string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active[group] += n
	if n > 0 {
		f.signals.Open++
	} else {
		f.signals.Close++
	}
	close(f.changed)
	f.changed = make(chan struct{})
}
func (f *Fixture) wait(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("active")
	if r.URL.Query().Get("event") != "" {
		value = r.URL.Query().Get("count")
	}
	target, err := strconv.Atoi(value)
	if err != nil || target < 0 {
		http.Error(w, "invalid active count", http.StatusBadRequest)
		return
	}
	event := r.URL.Query().Get("event")
	if event != "" {
		target, err = strconv.Atoi(r.URL.Query().Get("count"))
		if err != nil || target < 0 {
			http.Error(w, "invalid event count", 400)
			return
		}
	}
	group := r.URL.Query().Get("group")
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	for {
		f.mu.Lock()
		count := 0
		for key, value := range f.active {
			if group == "" || strings.EqualFold(key, group) {
				count += value
			}
		}
		if event != "" {
			switch event {
			case "factory":
				count = f.signals.Factory
			case "open":
				count = f.signals.Open
			case "close":
				count = f.signals.Close
			case "dispose":
				count = f.signals.Dispose
			default:
				f.mu.Unlock()
				http.Error(w, "unknown event", 400)
				return
			}
		}
		changed := f.changed
		f.mu.Unlock()
		if count == target {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			http.Error(w, fmt.Sprintf("active=%d, want %d: %v", count, target, ctx.Err()), http.StatusRequestTimeout)
			return
		}
	}
}

type trackedSource struct {
	state     *observable.State[[]Item]
	fixture   *Fixture
	group     string
	resources *feedResources
}

func (s *trackedSource) Current(ctx context.Context) ([]Item, bool, error) {
	return s.state.Current(ctx)
}
func (s *trackedSource) Open(ctx context.Context) (observable.Stream[[]Item], error) {
	stream, err := s.state.Open(ctx)
	if err != nil {
		return stream, err
	}
	if s.resources != nil {
		s.resources.opened.Store(true)
	}
	s.fixture.delta(s.group, 1)
	return &trackedStream{Stream: stream, source: s}, nil
}

type trackedStream struct {
	observable.Stream[[]Item]
	source *trackedSource
	once   sync.Once
}

func (s *trackedStream) Close(ctx context.Context) error {
	err := s.Stream.Close(ctx)
	if err == nil {
		s.once.Do(func() {
			if s.source.resources != nil {
				s.source.resources.closed.Store(true)
			}
			s.source.fixture.delta(s.source.group, -1)
		})
	}
	return err
}
