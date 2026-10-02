// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"bytes"
	"errors"

	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// DecodeError exposes parser causes locally while publishing only safe findings.
type DecodeError struct{ Cause error }

// Error returns safe malformed-input text.
func (e *DecodeError) Error() string { return "The request body is invalid." }

// Unwrap retains inspectable parser/conversion errors.
func (e *DecodeError) Unwrap() error { return e.Cause }

// ValidationResults returns one safe malformedRequest finding.
func (e *DecodeError) ValidationResults() []validation.Result {
	return []validation.Result{{Severity: validation.Error, Message: e.Error(), Members: []string{}, Reason: validation.MalformedRequest}}
}
func decodeCommand[C any](body []byte) (any, error) {
	root := bytes.TrimSpace(body)
	if len(root) == 0 || root[0] != '{' {
		return nil, &DecodeError{Cause: errors.New("command body must be an object")}
	}
	var c C
	if err := serialization.Unmarshal(body, &c); err != nil {
		return nil, &DecodeError{Cause: err}
	}
	if nilValue(c) {
		return nil, &DecodeError{Cause: ErrInvalidRegistration}
	}
	return c, nil
}
