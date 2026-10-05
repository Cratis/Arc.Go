// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/cratis/arc.go/validation"
)

var (
	// ErrInvalidRegistration identifies invalid configuration.
	ErrInvalidRegistration = errors.New("invalid query registration")
	// ErrDuplicate identifies a repeated registration identity.
	ErrDuplicate = errors.New("duplicate query registration")
	// ErrFrozen identifies mutation after a successful Build.
	ErrFrozen = errors.New("query registry is frozen")
	// ErrMissingPerformer identifies an absent callback.
	ErrMissingPerformer = errors.New("missing query performer")
	// ErrResponseType identifies incompatible declared data types.
	ErrResponseType = errors.New("incompatible query response type")
	// ErrUnsupportedObservable identifies snapshot registrations of streams.
	ErrUnsupportedObservable = errors.New("observable queries are not supported by the snapshot pipeline")
	// ErrUnknownQuery identifies an unregistered logical name.
	ErrUnknownQuery = errors.New("unknown query")
	// ErrInvalidArguments identifies malformed argument declarations or values.
	ErrInvalidArguments = errors.New("invalid query arguments")
	// ErrInvalidSorting identifies an unsupported direction or active field.
	ErrInvalidSorting = errors.New("invalid query sorting")
	// ErrMalformedRequest identifies malformed reader syntax.
	ErrMalformedRequest = errors.New("malformed query request")
)

// RegistrationError retains a configuration category and logical identity.
type RegistrationError struct {
	Identity string
	Kind     error
}

func (e *RegistrationError) Error() string { return fmt.Sprintf("query %q: %v", e.Identity, e.Kind) }
func (e *RegistrationError) Unwrap() error { return e.Kind }

// ArgumentError describes missing or malformed caller input without publishing its value.
type ArgumentError struct {
	Name    string
	Type    reflect.Type
	Missing bool
	Cause   error
}

func (e *ArgumentError) Error() string { return fmt.Sprintf("invalid query argument %q", e.Name) }
func (e *ArgumentError) Unwrap() error { return e.Cause }

// ValidationResults returns safe transport-independent findings.
func (e *ArgumentError) ValidationResults() []validation.Result {
	reason, message := "malformedRequest", "The query argument is malformed."
	if e.Missing {
		reason, message = "rule", "The query argument is required."
	}
	return []validation.Result{{Severity: validation.Error, Message: message, Members: []string{e.Name}, Reason: validation.Reason(reason)}}
}

// SortingError identifies a bad direction or unregistered sort field.
type SortingError struct {
	Field string
	Cause error
}

func (e *SortingError) Error() string { return "invalid query sorting" }
func (e *SortingError) Unwrap() error { return errors.Join(ErrInvalidSorting, e.Cause) }

// ValidationResults reports a safe sorting finding.
func (e *SortingError) ValidationResults() []validation.Result {
	message := "The query sorting is invalid."
	if e.Cause == ErrInvalidSorting && (e.Field == "sortDirection" || e.Field == "sorting.direction") {
		message = "The sort direction is not a recognized value."
	}
	return []validation.Result{{Severity: validation.Error, Message: message, Members: []string{e.Field}, Reason: "malformedRequest"}}
}

// ReadError distinguishes syntax errors (Malformed) from semantic reader findings.
// Hosting must map malformed QUERY syntax to 400 and apply no-store even on failure.
type ReadError struct {
	Malformed bool
	Cause     error
}

func (e *ReadError) Error() string { return "could not read query request" }
func (e *ReadError) Unwrap() error { return e.Cause }

// Is exposes the malformed category without adding it to the diagnostic causes.
func (e *ReadError) Is(target error) bool { return e.Malformed && target == ErrMalformedRequest }
