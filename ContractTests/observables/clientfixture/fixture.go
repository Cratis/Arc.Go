// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package clientfixture composes the real Arc host used by the locked client lane.
package clientfixture

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

// Item has the stock client's conventional collection identity.
type Item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type arguments struct {
	Group string `json:"group" query:"required"`
}

// Fixture owns immutable subject values and tracks actual stream cleanup.
// Control handlers never construct successful query results or transport frames.
type Fixture struct {
	App     *arc.Application
	Names   map[string]string
	states  map[string]*observable.State[[]Item]
	deny    atomic.Bool
	mu      sync.Mutex
	active  map[string]int
	changed chan struct{}
}

// New builds without starting a listener. The caller owns App.Serve and its join.
func New() (*Fixture, error) {
	f := &Fixture{Names: map[string]string{}, states: map[string]*observable.State[[]Item]{}, active: map[string]int{}, changed: make(chan struct{})}
	for _, group := range []string{"alpha", "beta", "pending", "nil", "guarded"} {
		var state *observable.State[[]Item]
		var err error
		switch group {
		case "pending":
			state, err = observable.NewPendingState(observable.SubjectOptions[[]Item]{Buffer: 16})
		case "nil":
			state, err = observable.NewState[[]Item](nil, observable.SubjectOptions[[]Item]{Buffer: 16})
		default:
			state, err = observable.NewState([]Item{{ID: "a", Title: group + "-old"}, {ID: "b", Title: group + "-gone"}}, observable.SubjectOptions[[]Item]{Buffer: 16})
		}
		if err != nil {
			return nil, err
		}
		f.states[group] = state
	}
	// Explicit host-owned anonymous fixture session, not browser cookie evidence.
	builder, err := arc.NewBuilder(arc.Options{HTTP: arc.HTTPOptions{ShutdownTimeout: 5 * time.Second}, Observable: arc.ObservableOptions{AnonymousOwner: func(context.Context, *http.Request) (string, error) { return "loopback-client-fixture", nil }}})
	if err != nil {
		return nil, err
	}
	err = queries.RegisterObservable[Item](builder, "All", queries.Function(func(_ context.Context, args arguments) (observable.Source[[]Item], error) {
		state, ok := f.states[args.Group]
		if !ok {
			return nil, fmt.Errorf("unknown fixture group")
		}
		return &trackedSource{state: state, fixture: f, group: args.Group}, nil
	}), queries.WithPath[arguments]("/items"), queries.WithAuthorization[arguments](metadata.Authorization{AllowAnonymous: true}))
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
	} {
		if err := builder.Handle(pattern, handler); err != nil {
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
	close(f.changed)
	f.changed = make(chan struct{})
}
func (f *Fixture) wait(w http.ResponseWriter, r *http.Request) {
	target, err := strconv.Atoi(r.URL.Query().Get("active"))
	if err != nil || target < 0 {
		http.Error(w, "invalid active count", http.StatusBadRequest)
		return
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
	state   *observable.State[[]Item]
	fixture *Fixture
	group   string
}

func (s *trackedSource) Current(ctx context.Context) ([]Item, bool, error) {
	return s.state.Current(ctx)
}
func (s *trackedSource) Open(ctx context.Context) (observable.Stream[[]Item], error) {
	stream, err := s.state.Open(ctx)
	if err != nil {
		return stream, err
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
		s.once.Do(func() { s.source.fixture.delta(s.source.group, -1) })
	}
	return err
}
