// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package correlation_test

import (
	"context"
	"testing"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/correlation"
	fcorrelation "github.com/cratis/fundamentals.go/correlation"
)

func TestSharedCorrelationContext(t *testing.T) {
	id, err := concepts.ParseUUID("00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		set  func(context.Context, correlation.ID) context.Context
		read func(context.Context) correlation.ID
	}{
		{"Arc to Fundamentals", correlation.WithID, fcorrelation.FromContext},
		{"Fundamentals to Arc", fcorrelation.WithID, correlation.FromContext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			if got := tc.read(parent); !got.IsZero() {
				t.Fatal("absent read generated an ID")
			}
			ctx := tc.set(parent, id)
			if got := tc.read(ctx); got != id {
				t.Fatalf("shared ID = %v, want %v", got, id)
			}
			zero := tc.set(ctx, correlation.ID{})
			if got := tc.read(zero); !got.IsZero() {
				t.Fatal("explicit zero did not shadow parent")
			}
			if got := tc.read(ctx); got != id {
				t.Fatal("child mutated parent")
			}
			cancel()
			if ctx.Err() != context.Canceled || zero.Err() != context.Canceled {
				t.Fatal("shared context lost cancellation")
			}
		})
	}
	// Mixed accessors must also shadow the other package's inherited value.
	ctx := fcorrelation.WithID(t.Context(), id)
	if got := fcorrelation.FromContext(correlation.WithID(ctx, correlation.ID{})); !got.IsZero() {
		t.Fatal("Arc zero did not shadow Fundamentals ID")
	}
	ctx = correlation.WithID(t.Context(), id)
	if got := correlation.FromContext(fcorrelation.WithID(ctx, fcorrelation.ID{})); !got.IsZero() {
		t.Fatal("Fundamentals zero did not shadow Arc ID")
	}
}

func TestSharedCorrelationNilContextPanics(t *testing.T) {
	for name, invoke := range map[string]func(){
		"WithID": func() {
			//nolint:staticcheck // Intentionally verify the documented nil-context panic.
			correlation.WithID(nil, correlation.ID{})
		},
		"FromContext": func() {
			//nolint:staticcheck // Intentionally verify the documented nil-context panic.
			correlation.FromContext(nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("nil context did not panic")
				}
			}()
			invoke()
		})
	}
}
