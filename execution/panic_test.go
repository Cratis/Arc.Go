// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cratis/arc.go/execution"
)

func TestCallbackAndDisposalPanicsAreJoinedAndRedacted(t *testing.T) {
	h := &holder{close: func(context.Context) error { panic("cleanup secret") }}
	err := execution.RunWithResources(context.Background(), openHolder(h), execution.Metadata{}, 0, func(context.Context, *execution.Scope) error { panic("callback secret") })
	var panicError *execution.PanicError
	if !errors.As(err, &panicError) || len(panicError.Stack) == 0 || panicError.Value != "callback secret" {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "secret") || h.closes.Load() != 1 {
		t.Fatal("leaked panic or resources")
	}
}

func TestOpenerPanicIsInspectable(t *testing.T) {
	_, err := execution.OpenScope(context.Background(), func(context.Context) (execution.Resources, error) { panic(42) })
	var panicError *execution.PanicError
	if !errors.As(err, &panicError) || panicError.Value != 42 {
		t.Fatal(err)
	}
}
