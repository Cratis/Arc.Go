// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errObserverWaitElapsed = errors.New("observer wait elapsed")

// observerWaitElapsed distinguishes the full polling window from parent cancellation.
func observerWaitElapsed(wait context.Context) bool {
	return errors.Is(context.Cause(wait), errObserverWaitElapsed)
}

// readEndedByWindow reports whether a failed poll read was ended by the wait
// window rather than by a defect. gRPC refuses to start a call whose deadline is
// already in the past by the wall clock and reports DeadlineExceeded, which can
// happen before the context's own timer has fired and wait.Err() turns non-nil.
// Such a read is the end of the window, not a read failure; this waits for the
// timer so context.Cause(wait) records why the window ended. Any other error, or
// a deadline error before the window's deadline, is a genuine read failure.
func readEndedByWindow(wait context.Context, err error) bool {
	if err == nil {
		return false
	}
	if wait.Err() != nil {
		return true
	}
	deadline, ok := wait.Deadline()
	if !ok || (!errors.Is(err, context.DeadlineExceeded) && status.Code(err) != codes.DeadlineExceeded) || time.Now().Before(deadline) {
		return false
	}
	<-wait.Done()
	return true
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
	// LastHandled, Next and Tail are the observer's kernel sequence numbers.
	LastHandled, Next, Tail uint64
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

// matchesChronicle4548FrozenTail reports whether a reactor stall is the
// Chronicle#4548 strand observed after its tail froze: the same idle, active,
// subscribed observer with no delivery and no failure, but whose own tail is
// below the event because the strand began before the event was appended.
//
// Kernel evidence (19.32.1, Observation debug logging, 2 of 80 runs): in both
// failures the reactor's catch-up job completed, the observer routed back into
// catch-up, logged "Found already running job" for the finishing job, and logged
// nothing further for that observer, which is the #4548 finishing-job reuse.
// The stranded observer stays in its catch-up state, so later appends never
// update its tail; it reported Next == Tail == 1 and LastHandled 0 while the
// event was at position 2. The variant requires Next == Tail so an observer that
// knows of events it has not processed, or that has moved past its tail, is not
// mistaken for this strand. It applies to reactors only: projections have not
// shown it.
func (s reactorStall) matchesChronicle4548FrozenTail() bool {
	behind := s.LastHandled == unavailable || s.LastHandled < s.Position
	frozen := s.Tail != unavailable && s.Tail < s.Position && s.Next == s.Tail
	return s.WaitElapsed && s.Delivered == 0 && s.UnresolvedFailures == 0 && s.Active && s.Subscribed && behind && frozen
}

// projectionLag is the observable state of a projection whose read model did
// not reach the expected value before its deadline.
type projectionLag struct {
	// Observer is the projection observer's kernel state; Delivered is always zero.
	Observer reactorStall
	// ModelExists reports whether the read-model instance exists.
	ModelExists bool
	// ModelLastHandled is the instance's reported position, unavailable when absent.
	ModelLastHandled uint64
}

// matchesChronicle4583 reports whether a projection lag is the kernel skip from
// https://github.com/Cratis/Chronicle/issues/4583: after the full polling window
// the active, subscribed projection observer reports having handled the event
// (LastHandled at or past it, Next beyond it, the event within its tail) with no
// failed partition, yet the read model was never written for it. Catch-up moved
// the observer-wide position past an event its partition never handled.
//
// The read model must exist with a reported position below the event, or not
// exist at all; an instance at or past the event with the wrong value is a
// projection defect, and one without a reported position is not evidence of
// the skip. Kernel evidence (19.32.1): after the observer handled sequence 0,
// an append at 1 made Observing detect missed events and start a catch-up job
// from 1; that job completed without a step for the item-1 partition and the
// observer advanced to Next 2 while the read model stayed at LastHandled 0.
func (p projectionLag) matchesChronicle4583() bool {
	o := p.Observer
	advanced := o.LastHandled != unavailable && o.LastHandled >= o.Position && o.Next != unavailable && o.Next > o.Position
	known := o.Tail != unavailable && o.Tail >= o.Position
	stale := !p.ModelExists || (p.ModelLastHandled != unavailable && p.ModelLastHandled < o.Position)
	return o.WaitElapsed && o.Delivered == 0 && o.UnresolvedFailures == 0 && o.Active && o.Subscribed && advanced && known && stale
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
		{"frozen tail below the event", func(s reactorStall) reactorStall { s.Tail, s.Next = 1, 1; return s }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.stall(stranded).matchesChronicle4548(); got != c.want {
				t.Fatalf("matchesChronicle4548() = %t, want %t for %+v", got, c.want, c.stall(stranded))
			}
		})
	}
}

func TestReactorStallMatchesChronicle4548FrozenTailOnlyForTheStrandedTail(t *testing.T) {
	// The state captured from a local 19.32.1 repro of the stranded reactor.
	frozen := reactorStall{WaitElapsed: true, Position: 2, Active: true, Subscribed: true, LastHandled: 0, Next: 1, Tail: 1}
	cases := []struct {
		name  string
		stall func(reactorStall) reactorStall
		want  bool
	}{
		{"tail frozen below the event", func(s reactorStall) reactorStall { return s }, true},
		{"nothing handled yet", func(s reactorStall) reactorStall { s.LastHandled = unavailable; return s }, true},
		{"parent context exhausted", func(s reactorStall) reactorStall { s.WaitElapsed = false; return s }, false},
		{"delivered to Arc", func(s reactorStall) reactorStall { s.Delivered = 1; return s }, false},
		{"failure recorded", func(s reactorStall) reactorStall { s.UnresolvedFailures = 1; return s }, false},
		{"observer inactive", func(s reactorStall) reactorStall { s.Active = false; return s }, false},
		{"client unsubscribed", func(s reactorStall) reactorStall { s.Subscribed = false; return s }, false},
		{"observer advanced", func(s reactorStall) reactorStall { s.LastHandled = 2; return s }, false},
		{"tail unavailable", func(s reactorStall) reactorStall { s.Tail = unavailable; return s }, false},
		{"next behind the tail", func(s reactorStall) reactorStall { s.Next = 0; return s }, false},
		{"next past the tail", func(s reactorStall) reactorStall { s.Next = 2; return s }, false},
		{"tail reaches the event", func(s reactorStall) reactorStall { s.Tail, s.Next = 2, 2; return s }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.stall(frozen).matchesChronicle4548FrozenTail(); got != c.want {
				t.Fatalf("matchesChronicle4548FrozenTail() = %t, want %t for %+v", got, c.want, c.stall(frozen))
			}
		})
	}
}

func TestProjectionLagMatchesChronicle4583OnlyForTheSkippedEvent(t *testing.T) {
	// The state captured from a local 19.32.1 repro of the skipped event.
	skipped := projectionLag{
		Observer:    reactorStall{WaitElapsed: true, Position: 1, Active: true, Subscribed: true, LastHandled: 1, Next: 2, Tail: 1},
		ModelExists: true, ModelLastHandled: 0,
	}
	cases := []struct {
		name string
		lag  func(projectionLag) projectionLag
		want bool
	}{
		{"read model behind the handled event", func(p projectionLag) projectionLag { return p }, true},
		{"read model never written", func(p projectionLag) projectionLag {
			p.ModelExists, p.ModelLastHandled = false, unavailable
			return p
		}, true},
		{"observer past the event", func(p projectionLag) projectionLag {
			p.Observer.LastHandled, p.Observer.Next, p.Observer.Tail = 3, 4, 3
			return p
		}, true},
		{"parent context exhausted", func(p projectionLag) projectionLag { p.Observer.WaitElapsed = false; return p }, false},
		{"failure recorded", func(p projectionLag) projectionLag { p.Observer.UnresolvedFailures = 1; return p }, false},
		{"delivery counted", func(p projectionLag) projectionLag { p.Observer.Delivered = 1; return p }, false},
		{"observer inactive", func(p projectionLag) projectionLag { p.Observer.Active = false; return p }, false},
		{"observer unsubscribed", func(p projectionLag) projectionLag { p.Observer.Subscribed = false; return p }, false},
		{"observer behind the event (Chronicle#4548)", func(p projectionLag) projectionLag {
			p.Observer.LastHandled, p.Observer.Next = 0, 1
			return p
		}, false},
		{"nothing handled", func(p projectionLag) projectionLag { p.Observer.LastHandled = unavailable; return p }, false},
		{"next not past the event", func(p projectionLag) projectionLag { p.Observer.Next = 1; return p }, false},
		{"next unavailable", func(p projectionLag) projectionLag { p.Observer.Next = unavailable; return p }, false},
		{"tail below the event", func(p projectionLag) projectionLag { p.Observer.Tail = 0; return p }, false},
		{"tail unavailable", func(p projectionLag) projectionLag { p.Observer.Tail = unavailable; return p }, false},
		{"read model at the event with the wrong value", func(p projectionLag) projectionLag { p.ModelLastHandled = 1; return p }, false},
		{"read model past the event", func(p projectionLag) projectionLag { p.ModelLastHandled = 2; return p }, false},
		{"read model without a reported position", func(p projectionLag) projectionLag { p.ModelLastHandled = unavailable; return p }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.lag(skipped).matchesChronicle4583(); got != c.want {
				t.Fatalf("matchesChronicle4583() = %t, want %t for %+v", got, c.want, c.lag(skipped))
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

// clockPastDeadline reports a deadline that the clock has already reached while
// its context's timer has not fired, as gRPC observes at the window boundary.
type clockPastDeadline struct {
	context.Context
	deadline time.Time
}

func (c clockPastDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestReadEndedByWindowTreatsTheBoundaryDeadlineAsTheWindowEnd(t *testing.T) {
	rpcDeadline := fmt.Errorf("chronicle: read-model read failed: %w", context.DeadlineExceeded)
	cases := []struct {
		name        string
		err         error
		clockPassed bool
		cancel      bool
		want        bool
		wantElapsed bool
	}{
		{"read succeeded", nil, true, false, false, false},
		{"deadline error once the clock passed the window", rpcDeadline, true, false, true, true},
		{"deadline error before the window ends", rpcDeadline, false, false, false, false},
		{"raw gRPC deadline error at the window deadline", status.Error(codes.DeadlineExceeded, "context deadline exceeded"), true, false, true, true},
		{"raw gRPC deadline error before the window ends", status.Error(codes.DeadlineExceeded, "context deadline exceeded"), false, false, false, false},
		{"other error once the clock passed the window", errors.New("read refused"), true, false, false, false},
		{"parent cancelled", errors.New("read refused"), false, true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancelParent := context.WithCancel(context.Background())
				defer cancelParent()
				timed, cancel := context.WithTimeoutCause(parent, 15*time.Second, errObserverWaitElapsed)
				defer cancel()
				wait := context.Context(timed)
				if c.clockPassed {
					wait = clockPastDeadline{Context: timed, deadline: time.Now()}
				}
				if c.cancel {
					cancelParent()
				}
				if got := readEndedByWindow(wait, c.err); got != c.want {
					t.Fatalf("readEndedByWindow() = %t, want %t", got, c.want)
				}
				if got := observerWaitElapsed(timed); got != c.wantElapsed {
					t.Fatalf("observerWaitElapsed() = %t, want %t; cause = %v", got, c.wantElapsed, context.Cause(timed))
				}
			})
		})
	}
}
