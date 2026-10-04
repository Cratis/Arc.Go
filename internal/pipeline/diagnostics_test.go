// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline_test

import (
	"context"
	"testing"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/validation"
)

func TestDiagnosticOutcomePrecedenceUsesOnlyFinalFlagsAndReasons(t *testing.T) {
	for _, test := range []struct {
		name                                      string
		authorized, exceptions, failed, cancelled bool
		reason                                    validation.Reason
		want                                      observability.Outcome
	}{
		{"authorization before exception and cancellation", false, true, true, true, validation.ConstraintViolation, observability.Authorization},
		{"error before validation", true, true, false, false, validation.ConstraintViolation, observability.Error},
		{"cancelled exception before validation", true, true, true, true, validation.Rule, observability.Cancelled},
		{"validation error uses finalized findings", true, false, true, true, validation.Rule, observability.Validation},
		{"append error uses finalized reasons", true, false, true, false, validation.ConstraintViolation, observability.AppendRejected},
		{"error without classified result", true, false, true, false, "", observability.Error},
		{"append reason", true, false, false, false, validation.ConcurrencyViolation, observability.AppendRejected},
		{"open reason remains validation", true, false, false, false, "SECRET unknown reason", observability.Validation},
		{"cancellation does not replace successful result", true, false, false, true, "", observability.Success},
	} {
		t.Run(test.name, func(t *testing.T) {
			var findings []validation.Result
			if test.reason != "" {
				findings = []validation.Result{{Reason: test.reason, State: func() { panic("must not inspect") }}}
			}
			got := boundary.Outcome(test.authorized, test.exceptions, test.failed, test.cancelled, findings)
			if got != test.want {
				t.Fatal(got, test.want)
			}
		})
	}
}

type diagnosticSource struct{ recorder *observability.Recorder }

func (s diagnosticSource) Diagnostics() *observability.Recorder { return s.recorder }

func TestDiagnosticTokenForwardsCancellationButIsClearedForCallbacks(t *testing.T) {
	recorder, err := observability.NewRecorder(observability.Options{})
	if err != nil {
		t.Fatal(err)
	}
	source := diagnosticSource{recorder}
	ctx, outer := boundary.Begin(context.Background(), source, observability.Command, "unknown", observability.Unknown, observability.Completed)
	ctx, inner := boundary.Begin(ctx, source, observability.Command, "unknown", observability.Unknown, observability.Completed)
	inner.Finish(observability.Cancelled)
	_, nested := boundary.Begin(boundary.ClearDiagnostics(ctx), source, observability.Command, "unknown", observability.Unknown, observability.Completed)
	nested.Finish(observability.Success)
	outer.Finish(observability.Error)
	s := recorder.Snapshot()
	if len(s.Events) != 2 || s.Events[0].Outcome != observability.Success || s.Events[1].Outcome != observability.Cancelled {
		t.Fatal(s)
	}
}
