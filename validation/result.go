// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package validation defines client-visible Arc validation findings, not validators.
package validation

import (
	"encoding/json"

	"github.com/cratis/arc.go/serialization"
)

// Severity is Arc's numeric wire severity. Every retained finding makes a result
// invalid, regardless of severity; filtering belongs to the later pipeline.
type Severity int32

const (
	// Unknown is severity zero.
	Unknown Severity = iota
	// Information is informational feedback.
	Information
	// Warning is warning feedback.
	Warning
	// Error is an error finding.
	Error
)

// Reason is an open wire string; unknown future reasons remain valid.
type Reason string

const (
	// Rule identifies application-authored validation.
	Rule Reason = "rule"
	// ConcurrencyViolation identifies a concurrency rejection.
	ConcurrencyViolation Reason = "concurrencyViolation"
	// ConstraintViolation identifies an event-store constraint rejection.
	ConstraintViolation Reason = "constraintViolation"
	// ValidatorFailed identifies a validator that could not run.
	ValidatorFailed Reason = "validatorFailed"
	// DependencyUnavailable identifies a missing required read model.
	DependencyUnavailable Reason = "dependencyUnavailable"
	// MalformedRequest identifies invalid client input.
	MalformedRequest Reason = "malformedRequest"
)

// Result is one validation finding. Members use wire names, including indexed paths.
// State is borrowed application data; callers must not mutate it during encoding.
// A zero Reason marshals as "rule". Nil Members marshals as [], not null.
type Result struct {
	// Severity is the numeric importance of the finding.
	Severity Severity `json:"severity"`
	// Message is safe, client-visible feedback (never exception-redacted).
	Message string `json:"message"`
	// Members identifies affected wire members; nil means no member.
	Members []string `json:"members"`
	// State is optional application-owned data. Typed nil values are omitted.
	State any `json:"state,omitempty"`
	// Reason is the machine-readable open category; empty means Rule.
	Reason Reason `json:"reason"`
	// ReasonDetail optionally identifies a specific constraint or other cause.
	ReasonDetail *string `json:"reasonDetail,omitempty"`
}

// MarshalJSON implements the Arc validation envelope.
func (r Result) MarshalJSON() ([]byte, error) {
	members := r.Members
	if members == nil {
		members = []string{}
	}
	reason := r.Reason
	if reason == "" {
		reason = Rule
	}
	state, err := serialization.Marshal(r.State)
	if err != nil {
		return nil, err
	}
	if string(state) == "null" {
		state = nil
	}
	return json.Marshal(struct {
		Severity     Severity        `json:"severity"`
		Message      string          `json:"message"`
		Members      []string        `json:"members"`
		State        json.RawMessage `json:"state,omitempty"`
		Reason       Reason          `json:"reason"`
		ReasonDetail *string         `json:"reasonDetail,omitempty"`
	}{r.Severity, r.Message, members, state, reason, r.ReasonDetail})
}

// Clone copies a finding's members and reason detail. Application State is borrowed.
func (r Result) Clone() Result {
	r.Members = append([]string(nil), r.Members...)
	if r.ReasonDetail != nil {
		detail := *r.ReasonDetail
		r.ReasonDetail = &detail
	}
	return r
}
