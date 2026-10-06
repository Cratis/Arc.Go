// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/validation"
)

func TestBoundaryPreservesCallbackCancellationAndPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	failure := errors.New("callback")
	err := pipeline.Call(ctx, func(context.Context) error { cancel(); return failure })
	if !errors.Is(err, failure) || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	err = pipeline.Call(t.Context(), func(context.Context) error { panic("secret") })
	var panicError *execution.PanicError
	if !errors.As(err, &panicError) || len(panicError.Stack) == 0 {
		t.Fatal(err)
	}
	if err := pipeline.Call(ctx, func(context.Context) error { t.Fatal("called when canceled"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestClassificationKeepsJoinedInfrastructureFailure(t *testing.T) {
	cleanup := errors.New("cleanup")
	invocation := &validation.InvocationError{Cause: errors.New("private")}
	err := fmt.Errorf("outer: %w", errors.Join(validation.Reject(validation.Result{Severity: validation.Warning, Message: "business"}), cleanup, invocation))
	failure := pipeline.Classify(err)
	if len(failure.Findings) != 2 || len(failure.Exceptions) != 1 || failure.Exceptions[0] != cleanup {
		t.Fatalf("classified = %+v", failure)
	}
	if failure.Findings[1].Message != "The value could not be validated." {
		t.Fatal(failure.Findings)
	}
}

func TestCleanupIsBoundedAndRetainsValues(t *testing.T) {
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "value"))
	cancel()
	cleanup, stop, err := pipeline.CleanupContext(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	deadline, ok := cleanup.Deadline()
	if cleanup.Err() != nil || !ok || time.Until(deadline) > 30*time.Second || cleanup.Value(key{}) != "value" {
		t.Fatal("invalid cleanup context")
	}
	if _, _, err := pipeline.CleanupContext(ctx, -1); !errors.Is(err, execution.ErrInvalidArgument) {
		t.Fatal(err)
	}
}
