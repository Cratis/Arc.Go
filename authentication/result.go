// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authentication

import "github.com/cratis/arc.go/identity"

// Result is immutable; zero is anonymous. Success and failure are terminal.
type Result struct {
	principal identity.Principal
	failure   *Failure
}

// Anonymous returns the nonterminal anonymous result.
func Anonymous() Result { return Result{} }

// Authenticated returns success only for a trusted authenticated snapshot.
func Authenticated(principal identity.Principal) (Result, error) {
	if !principal.IsAuthenticated() {
		return Result{}, ErrInvalidPrincipal
	}
	return Result{principal: principal}, nil
}

// Failed records a terminal credential rejection. reason is local diagnostics.
func Failed(reason string) Result { return Result{failure: &Failure{reason: reason}} }

// Principal returns the trusted authenticated principal and presence.
func (r Result) Principal() (identity.Principal, bool) {
	return r.principal, r.principal.IsAuthenticated()
}

// Failure returns immutable diagnostic failure data or nil.
func (r Result) Failure() *Failure { return r.failure }

// Failure represents rejected credentials, not an infrastructure error.
type Failure struct{ reason string }

// Error returns generic text; it never discloses the diagnostic reason.
func (e *Failure) Error() string { return ErrFailed.Error() }

// Reason returns local diagnostics, not automatically client-visible text.
func (e *Failure) Reason() string { return e.reason }

// Unwrap identifies ErrFailed.
func (e *Failure) Unwrap() error { return ErrFailed }
