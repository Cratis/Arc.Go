// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
)

func TestHTTPForwardedAttemptCompletesBeforeConsumptionAndCallbackIsFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder, err := observability.NewRecorder(observability.Options{})
		if err != nil {
			t.Fatal(err)
		}
		source := diagnosticSource{recorder}
		_, ingress := boundary.Begin(t.Context(), source, observability.Query, "unknown", observability.ObservableTransport, observability.Opening)
		time.Sleep(time.Second)
		ctx := boundary.ForwardDiagnostics(t.Context(), ingress)
		if err := boundary.Call(ctx, func(ctx context.Context) error {
			_, nested := boundary.Begin(ctx, source, observability.Query, "unknown", observability.SnapshotTransport, observability.Completed)
			nested.Finish(observability.Success)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		_, opening := boundary.Begin(ctx, source, observability.Query, "unknown", observability.ObservableTransport, observability.Opening)
		opening.Finish(observability.Success)
		if len(recorder.Snapshot().Events) != 2 {
			t.Fatal("opening waited for HTTP consumption", recorder.Snapshot())
		}
		time.Sleep(10 * time.Second)
		ingress.FinishForwarded(observability.Error)
		snapshot := recorder.Snapshot()
		if len(snapshot.Events) != 2 || snapshot.Events[1].Phase != observability.Opening || snapshot.Events[1].Seconds != 1 {
			t.Fatal(snapshot)
		}
	})
}
