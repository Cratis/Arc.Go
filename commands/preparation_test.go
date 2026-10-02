// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/validation"
)

func TestPreparationControlsAndValidateExclusion(t *testing.T) {
	for _, tc := range []struct {
		name             string
		preparation      commands.Preparation[int]
		handled, success bool
		wantErr          error
	}{
		{"payload", commands.Provided(42), true, true, nil},
		{"advisory warning", commands.ProvidedWith(42, commands.WithValidationResults(correlation.ID{}, validation.Result{Severity: validation.Warning})), true, true, nil},
		{"error blocks", commands.ProvidedWith(42, commands.WithValidationResults(correlation.ID{}, validation.Result{Severity: validation.Error})), false, false, nil},
		{"success stop", commands.StopProviding[int](commands.Success(correlation.ID{})), false, true, nil},
		{"zero control denies", commands.ProvidedWith(42, commands.Result[commands.NoResponse]{}), false, false, nil},
		{"zero preparation", commands.Preparation[int]{}, false, false, commands.ErrInvalidPreparation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provided, handled := 0, false
			var r commands.Registry
			must(t, commands.Register[Clear](&r, commands.WithPreparation(func(Clear, context.Context) (commands.Preparation[int], error) {
				provided++
				return tc.preparation, nil
			}, func(_ Clear, _ context.Context, p int) (int, error) {
				handled = true
				if p != 42 {
					t.Fatal(p)
				}
				return p, nil
			})))
			p := build(t, &r, commands.PipelineOptions{})
			validated, err := p.Validate(t.Context(), Clear{})
			must(t, err)
			if !validated.IsSuccess() || provided != 0 || handled {
				t.Fatal("Validate invoked preparation")
			}
			result, err := p.Execute(t.Context(), Clear{})
			if !errors.Is(err, tc.wantErr) || result.IsSuccess() != tc.success || handled != tc.handled {
				t.Fatalf("result=%+v err=%v handled=%v", result.Details(), err, handled)
			}
		})
	}
}
func TestProvideErrorNeverSuppliesZeroPayload(t *testing.T) {
	cause := errors.New("unavailable")
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.WithProvide(func(Clear, context.Context) (int, error) { return 0, cause }, func(Clear, context.Context, int) (int, error) { t.Fatal("handled failed payload"); return 0, nil })))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Clear{})
	if !errors.Is(err, cause) || !result.HasExceptions() {
		t.Fatal(result.Details(), err)
	}
}
