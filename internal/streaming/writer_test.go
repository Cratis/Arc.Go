// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/internal/streaming"
)

func TestWriterCopiesFramesAndAcknowledgesOnlyAfterWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global, err := streaming.NewBudget(16)
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan []byte, 1)
		release := make(chan struct{})
		w, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{Application: global}, func(_ context.Context, frame []byte) error {
			started <- slices.Clone(frame)
			<-release
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		frame := []byte("immutable")
		delivered := make(chan error, 1)
		go func() { delivered <- w.Deliver(t.Context(), frame, nil) }()
		synctest.Wait()
		if w.RetainedBytes() != 9 || global.Used() != 9 {
			t.Fatal(w.RetainedBytes(), global.Used())
		}
		frame[0] = 'x'
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		if got := <-started; string(got) != "immutable" {
			t.Fatal(string(got))
		}
		synctest.Wait()
		select {
		case err := <-delivered:
			t.Fatal("queue admission acknowledged as write", err)
		default:
		}
		if global.Used() != 9 {
			t.Fatal("in-flight bytes released early")
		}
		close(release)
		if err := <-delivered; err != nil {
			t.Fatal(err)
		}
		if global.Used() != 0 || w.RetainedBytes() != 0 {
			t.Fatal("reservations retained after acknowledgement")
		}
		if err := w.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-run; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestWriterLegacyFenceWaitsForInflightPredecessorAndDropsObsoleteQueuedJobs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owners, err := streaming.NewSubscriptions(t.Context(), streaming.SubscriptionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		old, _, err := owners.Subscribe("q", nil)
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan string, 4)
		release := make(chan struct{})
		w, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{Owners: owners}, func(_ context.Context, frame []byte) error {
			started <- string(frame)
			if string(frame) == "old" {
				<-release
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		oldDelivery := make(chan error, 1)
		go func() { oldDelivery <- w.Deliver(old.Context(), []byte("old"), old) }()
		if got := <-started; got != "old" {
			t.Fatal(got)
		}
		obsoleteDelivery := make(chan error, 1)
		go func() { obsoleteDelivery <- w.Deliver(t.Context(), []byte("obsolete queued"), old) }()
		synctest.Wait()
		next, replaced, err := owners.Subscribe("q", nil)
		if err != nil || replaced != old {
			t.Fatal(err)
		}
		fenced := make(chan error, 1)
		go func() { fenced <- w.Fence(next.Context()) }()
		synctest.Wait()
		select {
		case err := <-fenced:
			t.Fatal("fence passed an in-flight predecessor", err)
		default:
		}
		if err := w.Deliver(t.Context(), []byte("late old"), old); !errors.Is(err, streaming.ErrObsolete) {
			t.Fatal(err)
		}
		close(release)
		if err := <-fenced; err != nil {
			t.Fatal(err)
		}
		if err := <-oldDelivery; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := <-obsoleteDelivery; !errors.Is(err, streaming.ErrObsolete) {
			t.Fatal(err)
		}
		if err := w.DeliverTerminal(next.Context(), []byte("terminal successor"), next); err != nil {
			t.Fatal(err)
		}
		if got := <-started; got != "terminal successor" {
			t.Fatal(got)
		}
		if err := w.Deliver(t.Context(), []byte("behind terminal"), next); !errors.Is(err, streaming.ErrObsolete) {
			t.Fatal(err)
		}
		if err := w.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-run
		owners.Joined(old)
		owners.Joined(next)
	})
}

func TestWriterCloseTimeoutRetainsInflightBytesUntilActualJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global, err := streaming.NewBudget(10)
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{})
		release := make(chan struct{})
		w, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{Application: global}, func(context.Context, []byte) error { close(started); <-release; return nil })
		if err != nil {
			t.Fatal(err)
		}
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		delivered := make(chan error, 1)
		go func() { delivered <- w.Deliver(t.Context(), []byte("retained"), nil) }()
		<-started
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := w.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if global.Used() != 8 || w.RetainedBytes() != 8 {
			t.Fatal("cancellation released unjoined bytes", global.Used())
		}
		select {
		case <-w.Done():
			t.Fatal("cancel claimed callback joined")
		default:
		}
		close(release)
		if err := w.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if global.Used() != 0 {
			t.Fatal(global.Used())
		}
		if err := <-delivered; err == nil {
			t.Fatal("canceled delivery succeeded")
		}
		<-run
	})
}

func TestWriterQueueAndByteOverloadCloseRatherThanDrop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options streaming.WriterOptions
		want    error
	}{
		{"jobs", streaming.WriterOptions{MaxJobs: 1}, streaming.ErrWriterCapacity},
		{"connection bytes", streaming.WriterOptions{MaxQueuedBytes: 3}, streaming.ErrByteCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, err := streaming.NewWriter(t.Context(), tc.options, func(context.Context, []byte) error { t.Error("unstarted writer invoked"); return nil })
				if err != nil {
					t.Fatal(err)
				}
				first := make(chan error, 1)
				go func() { first <- w.Deliver(t.Context(), []byte("abc"), nil) }()
				synctest.Wait()
				if err := w.Deliver(t.Context(), []byte("d"), nil); !errors.Is(err, tc.want) {
					t.Fatal(err)
				}
				if err := <-first; err == nil {
					t.Fatal("dropped queued frame reported success")
				}
				if err := w.Close(t.Context()); !errors.Is(err, tc.want) {
					t.Fatal(err)
				}
				if w.RetainedBytes() != 0 {
					t.Fatal(w.RetainedBytes())
				}
			})
		})
	}
}

func TestWriterGlobalBudgetFailureLeavesOtherConnectionReservationsIntact(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		global, err := streaming.NewBudget(3)
		if err != nil {
			t.Fatal(err)
		}
		w1, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{Application: global}, func(context.Context, []byte) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		w2, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{Application: global}, func(context.Context, []byte) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		first := make(chan error, 1)
		go func() { first <- w1.Deliver(t.Context(), []byte("abc"), nil) }()
		synctest.Wait()
		if err := w2.Deliver(t.Context(), []byte("d"), nil); !errors.Is(err, streaming.ErrByteCapacity) {
			t.Fatal(err)
		}
		if global.Used() != 3 || w2.RetainedBytes() != 0 {
			t.Fatal(global.Used(), w2.RetainedBytes())
		}
		if err := w1.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-first; err == nil {
			t.Fatal("close acknowledged queued write")
		}
		if global.Used() != 0 {
			t.Fatal(global.Used())
		}
	})
}

func TestWriterFrameOversizeDoesNotCloseConnectionAndFailureJoinsQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("transport failed")
		w, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{MaxFrameBytes: 3}, func(context.Context, []byte) error { return failure })
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Deliver(t.Context(), []byte("oversize"), nil); !errors.Is(err, streaming.ErrFrameCapacity) {
			t.Fatal(err)
		}
		first, second := make(chan error, 1), make(chan error, 1)
		go func() { first <- w.Deliver(t.Context(), []byte("abc"), nil) }()
		synctest.Wait()
		go func() { second <- w.Deliver(t.Context(), []byte("def"), nil) }()
		synctest.Wait()
		if err := w.Run(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if err := <-first; !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if err := <-second; err == nil {
			t.Fatal("failure acknowledged unwritten queue")
		}
		if w.RetainedBytes() != 0 {
			t.Fatal(w.RetainedBytes())
		}
		if err := w.Close(t.Context()); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	})
}

func TestWriterKeepaliveUsesSoleLoopAndPanicDoesNotLeakReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pinged := make(chan struct{}, 1)
		w, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{KeepAliveInterval: time.Second, KeepAlive: func(context.Context) error { pinged <- struct{}{}; return nil }}, func(context.Context, []byte) error { panic("transport panic") })
		if err != nil {
			t.Fatal(err)
		}
		run := make(chan error, 1)
		go func() { run <- w.Run() }()
		<-pinged
		if err := w.Deliver(t.Context(), []byte("test"), nil); err == nil {
			t.Fatal("panic acknowledged")
		}
		if err := <-run; err == nil {
			t.Fatal("panic swallowed")
		}
		if w.RetainedBytes() != 0 {
			t.Fatal("panic leaked reservation")
		}
	})
}

func TestByteReservationIdempotentAndOverflowSafe(t *testing.T) {
	budget, err := streaming.NewBudget(10)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Acquire(9)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Acquire(9223372036854775807); !errors.Is(err, streaming.ErrByteCapacity) {
		t.Fatal(err)
	}
	reservation.Release()
	reservation.Release()
	if budget.Used() != 0 {
		t.Fatal(budget.Used())
	}
}
