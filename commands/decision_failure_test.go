// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/validation"
)

func TestDecisionFailuresCannotBeAllowedByValidationSeverity(t *testing.T) {
	for _, stage := range []string{"admission", "acquisition", "check", "enrollment", "provided"} {
		t.Run(stage, func(t *testing.T) {
			f := decisionFrameForTest(t, false)
			var acquired, enrolled atomic.Int32
			target, source := decisionSourceForTest(&acquired, &enrolled)
			cause := validation.Reject(validation.Result{Severity: validation.Warning, Message: "advisory"})
			var issued bool
			switch stage {
			case "admission":
				source.admit = func(context.Context) error { return cause }
			case "acquisition":
				source.acquire = func(context.Context) (any, error) { return nil, cause }
			case "check":
				source.check = func(context.Context, any) error { return cause }
			case "enrollment":
				source.enroll = func(context.Context, any) error { return cause }
			case "provided":
				source.check = func(context.Context, any) error {
					if issued {
						return cause
					}
					return nil
				}
			}
			callDecisionFrame(t, f, func(ctx context.Context, inv *Invocation) error {
				if err := beginDecisionReads(ctx, inv, decisionAdmissionForTest()); err != nil {
					return err
				}
				read, err := readDecision(ctx, inv, target, source)
				if stage == "provided" {
					if err != nil {
						return err
					}
					issued = true
					err = verifyDecision(ctx, inv, read)
				} else if read != nil {
					t.Error("refused read delivered a value")
				}
				if !errors.Is(err, cause) || !errors.Is(err, errDecisionRead) || len(boundary.Classify(err).Exceptions) == 0 {
					t.Fatalf("filterable acquisition failure: %v", err)
				}
				return nil
			})
		})
	}
}
