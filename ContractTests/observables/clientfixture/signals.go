// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package clientfixture

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/cratis/arc.go/observable"
)

// Shutdown is closed by the loopback test control, not by the Arc runtime.
func (f *Fixture) Shutdown() <-chan struct{} { return f.shutdown }

// Signals returns a consistent lifecycle snapshot.
func (f *Fixture) Signals() Signals { f.mu.Lock(); defer f.mu.Unlock(); return f.signals }

func (f *Fixture) signal(update func(*Signals)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	update(&f.signals)
	close(f.changed)
	f.changed = make(chan struct{})
}

type feedResources struct {
	fixture *Fixture
	opened  atomic.Bool
	closed  atomic.Bool
}

func (r *feedResources) ForGroup(_ context.Context, group string) (observable.Source[[]Item], error) {
	r.fixture.signal(func(s *Signals) { s.Factory++ })
	state, ok := r.fixture.states[group]
	if !ok {
		return nil, fmt.Errorf("unknown fixture group")
	}
	return &trackedSource{state: state, fixture: r.fixture, group: group, resources: r}, nil
}

func (r *feedResources) Close(context.Context) error {
	if r.opened.Load() && !r.closed.Load() {
		return fmt.Errorf("fixture resources disposed before source joined")
	}
	r.fixture.signal(func(s *Signals) { s.Dispose++ })
	return nil
}
