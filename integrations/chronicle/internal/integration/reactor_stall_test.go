// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import "testing"

// unavailable mirrors Chronicle's events.Unavailable sequence number.
const unavailable = ^uint64(0)

// reactorStall is the observable state of a reactor that did not reach an
// expected outcome before its deadline.
type reactorStall struct {
	// Position is the sequence number of the event the reactor should handle.
	Position uint64
	// Delivered counts client-side invocations for the event's source.
	Delivered int
	// UnresolvedFailures counts the observer's unresolved failed partitions.
	UnresolvedFailures int
	// Active and Subscribed report the kernel's observer state.
	Active, Subscribed bool
	// LastHandled and Tail are the observer's kernel sequence numbers.
	LastHandled, Tail uint64
}

// matchesChronicle4548 reports whether a stall is the kernel strand from
// https://github.com/Cratis/Chronicle/issues/4548: the kernel knows the event,
// keeps the observer active and subscribed behind it, records no failure, and
// never delivers it to the client. Any delivery, failure, inactive or
// unsubscribed observer, or advanced observer is a different defect.
func (s reactorStall) matchesChronicle4548() bool {
	behind := s.LastHandled == unavailable || s.LastHandled < s.Position
	known := s.Tail != unavailable && s.Tail >= s.Position
	return s.Delivered == 0 && s.UnresolvedFailures == 0 && s.Active && s.Subscribed && behind && known
}

func TestReactorStallMatchesChronicle4548OnlyForTheKernelStrand(t *testing.T) {
	// The state captured from a local 19.29.4 repro of the stranded reactor.
	stranded := reactorStall{Position: 2, Active: true, Subscribed: true, LastHandled: 0, Tail: 2}
	cases := []struct {
		name  string
		stall func(reactorStall) reactorStall
		want  bool
	}{
		{"stranded behind the tail", func(s reactorStall) reactorStall { return s }, true},
		{"nothing handled yet", func(s reactorStall) reactorStall { s.LastHandled = unavailable; return s }, true},
		{"delivered to Arc", func(s reactorStall) reactorStall { s.Delivered = 1; return s }, false},
		{"failure recorded", func(s reactorStall) reactorStall { s.UnresolvedFailures = 1; return s }, false},
		{"observer inactive", func(s reactorStall) reactorStall { s.Active = false; return s }, false},
		{"client unsubscribed", func(s reactorStall) reactorStall { s.Subscribed = false; return s }, false},
		{"observer advanced", func(s reactorStall) reactorStall { s.LastHandled = 2; return s }, false},
		{"event unknown to observer", func(s reactorStall) reactorStall { s.Tail = 1; return s }, false},
		{"tail unavailable", func(s reactorStall) reactorStall { s.Tail = unavailable; return s }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.stall(stranded).matchesChronicle4548(); got != c.want {
				t.Fatalf("matchesChronicle4548() = %t, want %t for %+v", got, c.want, c.stall(stranded))
			}
		})
	}
}
