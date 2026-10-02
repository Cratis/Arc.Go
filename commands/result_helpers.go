// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"errors"
	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/correlation"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// Unauthorized produces a denied fragment with caller-authored safe feedback.
func Unauthorized(id correlation.ID, reason string) Result[NoResponse] {
	return NewResult(Details{CorrelationID: id, AuthorizationFailureReason: reason}, serialization.Optional[NoResponse]{})
}

// InvalidBody produces a safe malformedRequest finding.
func InvalidBody(id correlation.ID) Result[NoResponse] {
	return WithValidationResults(id, (&DecodeError{}).ValidationResults()...)
}

// WithValidationResults produces an authorized validation fragment.
func WithValidationResults(id correlation.ID, findings ...validation.Result) Result[NoResponse] {
	return NewResult(Details{CorrelationID: id, Authorized: true, ValidationResults: findings}, serialization.Optional[NoResponse]{})
}

// FromError classifies joined failures and always redacts production exceptions.
// It retains no response. The caller still owns and may inspect the original error.
func FromError[R any](id correlation.ID, err error) Result[R] {
	d := Details{CorrelationID: id, Authorized: true}
	failure := boundary.Classify(err)
	d.ValidationResults = failure.Findings
	for _, exception := range failure.Exceptions {
		if errors.Is(exception, authorization.ErrDenied) {
			d.Authorized = false
			if d.AuthorizationFailureReason == "" {
				d.AuthorizationFailureReason = "authorization denied"
			}
			continue
		}
		d.ExceptionMessages = append(d.ExceptionMessages, boundary.InternalErrorMessage)
	}
	return NewResult(d, serialization.Optional[R]{})
}

// Merge ANDs authorization, appends diagnostics and retains the outer correlation
// and first nonempty denial reason. Any failure removes response presence.
func Merge[R any](result Result[R], fragments ...Result[NoResponse]) Result[R] {
	d := result.Details()
	for _, fragment := range fragments {
		f := fragment.Details()
		d.Authorized = d.Authorized && f.Authorized
		d.ValidationResults = append(d.ValidationResults, f.ValidationResults...)
		d.ExceptionMessages = append(d.ExceptionMessages, f.ExceptionMessages...)
		if d.AuthorizationFailureReason == "" {
			d.AuthorizationFailureReason = f.AuthorizationFailureReason
		}
		if f.ExceptionStackTrace != "" {
			if d.ExceptionStackTrace != "" {
				d.ExceptionStackTrace += "\n"
			}
			d.ExceptionStackTrace += f.ExceptionStackTrace
		}
	}
	response := serialization.Optional[R]{}
	if value, ok := result.Response(); ok {
		response = serialization.Some(value)
	}
	return NewResult(d, response)
}
