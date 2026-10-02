// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation

import "errors"

var (
	// ErrRejected identifies an application rejection.
	ErrRejected = errors.New("validation rejected")
	// ErrValidatorFailed identifies an unexpected validator failure.
	ErrValidatorFailed = errors.New("validator failed")
	// ErrInvalidSeverity identifies unsupported numeric severities.
	ErrInvalidSeverity = errors.New("invalid validation severity")
	// ErrInvalidValidator identifies a nil callback or context.
	ErrInvalidValidator = errors.New("invalid validator")
)

// Failure is a business rejection or redacted invocation failure. Findings are
// returned as independent copies; their application State remains borrowed.
type Failure interface {
	error
	ValidationResults() []Result
}

type rejection struct{ results []Result }

func (e *rejection) Error() string               { return ErrRejected.Error() }
func (e *rejection) Unwrap() error               { return ErrRejected }
func (e *rejection) ValidationResults() []Result { return cloneResults(e.results) }

// Reject copies nonempty findings into a Failure; no findings returns nil.
func Reject(results ...Result) error {
	if len(results) == 0 {
		return nil
	}
	return &rejection{results: cloneResults(results)}
}

// InvocationError exposes local diagnostics, never callback details in its text.
// It implements Failure with one safe Error finding.
type InvocationError struct {
	// Cause is an inspectable unexpected callback error.
	Cause error
	// Panic is the recovered panic value, for local diagnostics only.
	Panic any
}

// Error returns generic, client-safe text.
func (e *InvocationError) Error() string { return "The value could not be validated." }

// Unwrap preserves the failure category and original cause.
func (e *InvocationError) Unwrap() []error { return []error{ErrValidatorFailed, e.Cause} }

// ValidationResults returns a fresh, redacted finding with no State or members.
func (e *InvocationError) ValidationResults() []Result {
	return []Result{{Severity: Error, Message: "The value could not be validated.", Members: []string{}, Reason: ValidatorFailed}}
}
