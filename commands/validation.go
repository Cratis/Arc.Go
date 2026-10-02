// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type validatorEntry struct {
	invoke func(context.Context, *execution.Scope, any) ([]validation.Result, error)
}

func (f *frame) validate() {
	if !f.registration.withoutModel {
		var findings []validation.Result
		err := f.call(func(ctx context.Context, inv *Invocation) error {
			var err error
			findings, err = f.pipeline.options.Validation.Validate(ctx, inv.Scope(), f.snapshot.command)
			return err
		})
		f.merge(WithValidationResults(f.snapshot.correlation, findings...), true)
		f.fail(err, true)
		if !f.result.IsAuthorized() || f.result.HasExceptions() {
			return
		}
	}
	for _, validator := range f.registration.validators {
		var findings []validation.Result
		err := f.call(func(ctx context.Context, inv *Invocation) error {
			var err error
			findings, err = validator.invoke(ctx, inv.Scope(), f.snapshot.command)
			return err
		})
		f.merge(WithValidationResults(f.snapshot.correlation, findings...), true)
		f.fail(err, true)
		if !f.result.IsAuthorized() || f.result.HasExceptions() {
			return
		}
	}
}
