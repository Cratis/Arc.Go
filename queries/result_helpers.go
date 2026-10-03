// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"github.com/cratis/arc.go/correlation"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// Unauthorized creates a ready denied snapshot. Reason is not serialized because
// Arc's query envelope, unlike commands, has no denial-reason field.
func Unauthorized[R any](id correlation.ID, _ string) Result[R] {
	return NewResult(Details{CorrelationID: id, Ready: true}, serialization.Optional[R]{})
}

// WithValidationResults creates a ready, authorized validation failure.
func WithValidationResults[R any](id correlation.ID, findings ...validation.Result) Result[R] {
	return NewResult(Details{CorrelationID: id, Ready: true, Authorized: true, ValidationResults: findings}, serialization.Optional[R]{})
}

// FromError converts joined failures independently and always redacts exceptions.
// The caller retains the original error for local errors.Is/As inspection.
func FromError[R any](id correlation.ID, err error) Result[R] {
	failure := boundary.Classify(err)
	d := Details{CorrelationID: id, Ready: true, Authorized: true, ValidationResults: failure.Findings}
	for range failure.Exceptions {
		d.ExceptionMessages = append(d.ExceptionMessages, boundary.InternalErrorMessage)
	}
	return NewResult(d, serialization.Optional[R]{})
}

// Merge ANDs authorization and appends diagnostics, retaining outer correlation
// and data presence. Pipeline finalization, not this constructor, removes failed data.
func Merge[R any](outer Result[R], fragments ...Result[any]) Result[R] {
	d := outer.Details()
	for _, fragment := range fragments {
		f := fragment.Details()
		d.Authorized = d.Authorized && f.Authorized
		d.ValidationResults = append(d.ValidationResults, f.ValidationResults...)
		d.ExceptionMessages = append(d.ExceptionMessages, f.ExceptionMessages...)
		if d.ExceptionStackTrace == "" {
			d.ExceptionStackTrace = f.ExceptionStackTrace
		}
	}
	return NewResult(d, outer.data)
}
func finalize[R any](r Result[R], expose bool) Result[R] {
	d := r.Details()
	if !expose {
		for i := range d.ExceptionMessages {
			d.ExceptionMessages[i] = boundary.InternalErrorMessage
		}
		d.ExceptionStackTrace = ""
	}
	if !d.Authorized || len(d.ValidationResults) > 0 || len(d.ExceptionMessages) > 0 {
		d.Ready = true
		d.ChangeSet = nil
		return NewResult(d, serialization.Optional[R]{})
	}
	return NewResult(d, r.data)
}
