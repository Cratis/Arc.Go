// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization

// Decision is an immutable verdict. Zero denies; it is not a reusable access token.
type Decision struct {
	allowed bool
	reason  string
}

// Allow returns an allowed verdict.
func Allow() Decision { return Decision{allowed: true} }

// Deny returns a denied verdict with local diagnostic reason.
func Deny(reason string) Decision { return Decision{reason: reason} }

// IsAllowed reports the verdict.
func (d Decision) IsAllowed() bool { return d.allowed }

// Reason returns local diagnostics, not automatically client-visible text.
func (d Decision) Reason() string { return d.reason }

// Err returns nil for Allow, otherwise an ErrDenied wrapper.
func (d Decision) Err() error {
	if d.allowed {
		return nil
	}
	return &DeniedError{Reason: d.reason}
}

// DeniedError retains local diagnostic reason without including it in error text.
type DeniedError struct {
	// Reason is diagnostic application text, not safe HTTP response content.
	Reason string
}

// Error returns generic denial text.
func (e *DeniedError) Error() string { return ErrDenied.Error() }

// Unwrap identifies ErrDenied.
func (e *DeniedError) Unwrap() error { return ErrDenied }
