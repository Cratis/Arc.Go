// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

var errObserverWaitElapsed = errors.New("observer wait elapsed")

// observerWaitElapsed distinguishes the full polling window from parent cancellation.
func observerWaitElapsed(wait context.Context) bool {
	return errors.Is(context.Cause(wait), errObserverWaitElapsed)
}

// unavailable mirrors Chronicle's events.Unavailable sequence number.
const unavailable = ^uint64(0)

// reactorStall is the observable state of a reactor that did not reach an
// expected outcome before its deadline.
type reactorStall struct {
	// WaitElapsed reports that the full polling window, not the parent context, ended the wait.
	WaitElapsed bool
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
// https://github.com/Cratis/Chronicle/issues/4548: after the full polling window,
// the kernel knows the event,
// keeps the observer active and subscribed behind it, records no failure, and
// never delivers it to the client. Any delivery, failure, inactive or
// unsubscribed observer, or advanced observer is a different defect.
//
// Delivered is counted inside the reactor's Handle method, so a client-SDK
// defect in chronicle.go before Handle (for example a dropped or undecodable
// delivery) also leaves Delivered at zero and can match this signature. The
// signature narrows the kernel strand; it does not prove it. Running with
// ARC_CHRONICLE_RUN_KNOWN_FLAKES=1 turns the skip into a failure and so
// distinguishes the two. Projection observers have no client hook, so their
// Delivered is always zero and the check relies on the kernel state alone.
func (s reactorStall) matchesChronicle4548() bool {
	behind := s.LastHandled == unavailable || s.LastHandled < s.Position
	known := s.Tail != unavailable && s.Tail >= s.Position
	return s.WaitElapsed && s.Delivered == 0 && s.UnresolvedFailures == 0 && s.Active && s.Subscribed && behind && known
}

func TestReactorStallMatchesChronicle4548OnlyForTheKernelStrand(t *testing.T) {
	// The state captured from a local 19.29.4 repro of the stranded reactor.
	stranded := reactorStall{WaitElapsed: true, Position: 2, Active: true, Subscribed: true, LastHandled: 0, Tail: 2}
	cases := []struct {
		name  string
		stall func(reactorStall) reactorStall
		want  bool
	}{
		{"stranded behind the tail", func(s reactorStall) reactorStall { return s }, true},
		{"nothing handled yet", func(s reactorStall) reactorStall { s.LastHandled = unavailable; return s }, true},
		{"projection observer stranded in a fresh namespace", func(reactorStall) reactorStall {
			return reactorStall{WaitElapsed: true, Position: 5, Active: true, Subscribed: true, LastHandled: unavailable, Tail: 5}
		}, true},
		{"parent context exhausted", func(s reactorStall) reactorStall { s.WaitElapsed = false; return s }, false},
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

func TestObserverWaitElapsedRequiresItsOwnDeadline(t *testing.T) {
	cases := []struct {
		name   string
		parent func() (context.Context, context.CancelFunc)
		want   bool
	}{
		{"full window elapsed", func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		}, true},
		{"parent deadline exhausted", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Second)
		}, false},
		{"parent cancelled", func() (context.Context, context.CancelFunc) {
			parent, cancel := context.WithCancel(context.Background())
			cancel()
			return parent, cancel
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancelParent := c.parent()
				defer cancelParent()
				wait, cancel := context.WithTimeoutCause(parent, 15*time.Second, errObserverWaitElapsed)
				defer cancel()
				<-wait.Done()
				if got := observerWaitElapsed(wait); got != c.want {
					t.Fatalf("observerWaitElapsed() = %t, want %t; cause = %v", got, c.want, context.Cause(wait))
				}
			})
		})
	}
}
