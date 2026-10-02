// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation

import (
	"context"
	"errors"
	"reflect"
)

// Validator validates caller-owned input synchronously. Implementations must honor
// cancellation and support concurrent calls if shared across operations.
type Validator[T any] interface {
	Validate(context.Context, T) ([]Result, error)
}

// ValidatorFunc adapts a validation callback.
type ValidatorFunc[T any] func(context.Context, T) ([]Result, error)

// Validate invokes f without taking ownership of value.
func (f ValidatorFunc[T]) Validate(ctx context.Context, value T) ([]Result, error) {
	return f(ctx, value)
}

// Invoke copies findings (but borrows State), preserves wrapped Failure errors,
// and redacts unexpected errors/panics. Cancellation is not validation failure.
func Invoke[T any](ctx context.Context, validator Validator[T], value T) (results []Result, err error) {
	if ctx == nil || isNil(validator) {
		return nil, ErrInvalidValidator
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if p := recover(); p != nil {
			results = nil
			err = &InvocationError{Panic: p}
		}
		if canceled := ctx.Err(); canceled != nil {
			results = nil
			// Preserve a callback's original cancellation wrapper and identity.
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				err = canceled
			}
		}
	}()
	results, err = validator.Validate(ctx, value)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if err != nil {
		var failure Failure
		if errors.As(err, &failure) {
			if !validResults(results) || !validResults(failure.ValidationResults()) {
				return nil, &InvocationError{Cause: ErrInvalidSeverity}
			}
			return cloneResults(results), err
		}
		return nil, &InvocationError{Cause: err}
	}
	if !validResults(results) {
		return nil, &InvocationError{Cause: ErrInvalidSeverity}
	}
	return cloneResults(results), nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
func cloneResults(results []Result) []Result {
	if results == nil {
		return nil
	}
	copy := make([]Result, len(results))
	for i, result := range results {
		copy[i] = result.Clone()
	}
	return copy
}
func validResults(results []Result) bool {
	for _, result := range results {
		if !validSeverity(result.Severity) {
			return false
		}
	}
	return true
}
