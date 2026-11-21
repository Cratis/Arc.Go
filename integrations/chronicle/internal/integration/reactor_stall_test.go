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
