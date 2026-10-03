// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package chronicle

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/validation"
)

// CommitError exposes all raw server-side diagnostics. Findings never expose raw
// constraint Details; unknown constraint types and future append codes survive here.
type CommitError struct {
	Result CommitResult
	Cause  error
}

func (e *CommitError) Error() string { return "chronicle integration: commit failed" }
func (e *CommitError) Unwrap() error { return e.Cause }

// Failure maps a provider completion without treating a nil operation error as
// success. Store findings bypass Arc's input severity filtering.
func (r CommitResult) Failure(command any, operation error) error {
	var findings []validation.Result
	for _, v := range r.Constraints {
		name := v.Name
		finding := validation.Result{Severity: validation.Error, Reason: validation.ConstraintViolation, ReasonDetail: &name, Message: v.Message}
		if v.Property != "" {
			finding.Members = []string{wireMember(reflect.TypeOf(command), v.Property)}
		}
		findings = append(findings, finding)
	}
	for _, v := range r.Concurrency {
		state := struct {
			Source   EventSourceID `json:"eventSourceId"`
			Expected uint64        `json:"expectedEventSequenceNumber"`
			Actual   uint64        `json:"actualEventSequenceNumber"`
		}{v.Source, v.Expected, v.Actual}
		findings = append(findings, validation.Result{Severity: validation.Error, Reason: validation.ConcurrencyViolation, State: state, Message: fmt.Sprintf("Event source '%s' has new events since the command read it: expected %s, but it has %s. Read it again and resubmit.", v.Source, describePosition(v.Expected), describePosition(v.Actual))})
	}
	failure := errors.Join(validation.Reject(findings...), operation, errors.Join(r.Errors...))
	if r.Report.Disposition == commands.OutcomeUnknown || r.Report.Disposition == commands.MixedCommit {
		failure = errors.Join(failure, ErrUnknownOutcome)
	}
	if failure == nil && r.Report.Disposition == commands.NotCommitted {
		failure = commands.ErrCommitNotConfirmed
	}
	if failure == nil {
		return nil
	}
	return &CommitError{Result: r, Cause: failure}
}
func describePosition(p uint64) string {
	if p == ^uint64(0)-2 {
		return "no events"
	}
	if p >= ^uint64(0)-2 {
		return "an unknown position"
	}
	return fmt.Sprintf("events up to sequence number %d", p)
}
func wireMember(t reflect.Type, path string) string {
	parts := strings.Split(path, ".")
	for index, part := range parts {
		for t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		found := false
		if t != nil && t.Kind() == reflect.Struct {
			for _, field := range reflect.VisibleFields(t) {
				tag := strings.Split(field.Tag.Get("json"), ",")[0]
				if field.IsExported() && (field.Name == part || tag == part) && tag != "-" {
					if tag != "" {
						parts[index] = tag
					} else {
						parts[index] = camel(part)
					}
					t = field.Type
					found = true
					break
				}
			}
		}
		if !found {
			return camel(path)
		}
	}
	return strings.Join(parts, ".")
}
func camel(value string) string {
	chars := []rune(value)
	// Fundamentals' pinned C# policy preserves leading acronyms.
	if len(chars) > 0 && (len(chars) <= 1 || !unicode.IsUpper(chars[1])) {
		chars[0] = unicode.ToLower(chars[0])
	}
	return string(chars)
}
