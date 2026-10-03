// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/internal/streaming"
)

func TestBaselinesShareFrameAndApplicationBudgetsUntilExplicitRelease(t *testing.T) {
	global, err := streaming.NewBudget(100)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{MaxQueuedBytes: 100, MaxFrameBytes: 100, Application: global}, func(context.Context, []byte) error { t.Error("unexpected write"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	release, err := writer.ReserveBaseline(50)
	if err != nil {
		t.Fatal(err)
	}
	if writer.RetainedBytes() != 50 || global.Used() != 50 {
		t.Fatal("baseline not accounted")
	}
	if err := writer.Deliver(t.Context(), make([]byte, 60), nil); !errors.Is(err, streaming.ErrByteCapacity) {
		t.Fatal(err)
	}
	<-writer.Done()
	if writer.RetainedBytes() != 50 || global.Used() != 50 {
		t.Fatal("closing writer prematurely released caller baseline")
	}
	if _, err := writer.ReserveBaseline(1); !errors.Is(err, streaming.ErrWriterClosed) {
		t.Fatal(err)
	}
	release()
	release()
	if writer.RetainedBytes() != 0 || global.Used() != 0 {
		t.Fatal("baseline leak")
	}
}
func TestBaselineGlobalRejectionReleasesLocalReservation(t *testing.T) {
	global, err := streaming.NewBudget(10)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := streaming.NewWriter(t.Context(), streaming.WriterOptions{MaxQueuedBytes: 100, Application: global}, func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ReserveBaseline(20); !errors.Is(err, streaming.ErrByteCapacity) {
		t.Fatal(err)
	}
	<-writer.Done()
	if writer.RetainedBytes() != 0 || global.Used() != 0 {
		t.Fatal("rejected baseline leaked bytes")
	}
}
