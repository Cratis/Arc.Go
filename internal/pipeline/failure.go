// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package pipeline

import (
	"errors"

	"github.com/cratis/arc.go/validation"
)

// InternalErrorMessage is the production exception text for both pipelines.
const InternalErrorMessage = "An internal error occurred while processing the request. See server logs for details."

// Failure separates filterable findings from infrastructure errors. The original
// caller error must still be retained for errors.Is/As and local diagnostics.
type Failure struct {
	Findings   []validation.Result
	Exceptions []error
}

// Classify visits joined branches independently, including joins behind wrappers.
// InvocationError and other direct Failure implementations are terminal: their
// diagnostic causes must not be published again as unrelated exceptions.
func Classify(err error) Failure {
	var result Failure
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if failure, ok := err.(validation.Failure); ok {
			findings := failure.ValidationResults()
			for _, finding := range findings {
				if finding.Severity < validation.Unknown || finding.Severity > validation.Error {
					result.Findings = append(result.Findings, (&validation.InvocationError{Cause: validation.ErrInvalidSeverity}).ValidationResults()...)
					return
				}
			}
			for _, finding := range findings {
				result.Findings = append(result.Findings, finding.Clone())
			}
			return
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, branch := range joined.Unwrap() {
				visit(branch)
			}
			return
		}
		if cause := errors.Unwrap(err); cause != nil {
			visit(cause)
			return
		}
		result.Exceptions = append(result.Exceptions, err)
	}
	visit(err)
	return result
}
