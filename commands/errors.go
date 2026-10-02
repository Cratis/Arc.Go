// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import "errors"

var (
	// ErrInvalidRegistration identifies invalid configuration or input.
	ErrInvalidRegistration = errors.New("invalid command registration")
	// ErrDuplicate identifies duplicate types, identities, options or extensions.
	ErrDuplicate = errors.New("duplicate command registration")
	// ErrFrozen identifies mutation after a successful Build.
	ErrFrozen = errors.New("command registry frozen")
	// ErrMissingHandler identifies an absent supported handler.
	ErrMissingHandler = errors.New("command handler missing")
	// ErrResponseType identifies an incompatible or unknown response contract.
	ErrResponseType = errors.New("incompatible command response type")
	// ErrMultipleResponses identifies ambiguous unconsumed return values.
	ErrMultipleResponses = errors.New("multiple command responses")
	// ErrUnhandledEffect identifies an effect without a consumer.
	ErrUnhandledEffect = errors.New("unhandled command effect")
	// ErrInvalidPreparation identifies a zero preparation or missing payload.
	ErrInvalidPreparation = errors.New("invalid command preparation")
	// ErrNoContext identifies a missing command callback context.
	ErrNoContext = errors.New("command context unavailable")
	// ErrExecutionClosed identifies an expired executor or invocation.
	ErrExecutionClosed = errors.New("command execution closed")
	// ErrConcurrentExecution identifies overlapping use of a bound executor.
	ErrConcurrentExecution = errors.New("concurrent command execution")
	// ErrExecutionMismatch identifies an incompatible frame or validation-only use.
	ErrExecutionMismatch = errors.New("command execution mismatch")
)

// RegistrationError retains the affected identity and inspectable category.
type RegistrationError struct {
	Identity string
	Kind     error
}

// Error describes a registration failure.
func (e *RegistrationError) Error() string { return "command " + e.Identity + ": " + e.Kind.Error() }

// Unwrap exposes the category.
func (e *RegistrationError) Unwrap() error { return e.Kind }
