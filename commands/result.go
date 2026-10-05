// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package commands executes explicitly registered, model-bound Arc commands.
// Register composes typed adapters without reflective invocation or a required
// container. Build freezes metadata and extensions; Execute owns resources and
// completion, while Validate runs authorization and input validation only.
// Failed results never publish a response. Cleanup is not proof of rollback.
package commands

import (
	"encoding/json"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/internal/wire"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// NoResponse is the type argument for commands without a client response.
type NoResponse struct{}

// Details configures a finalized result. Zero denies authorization. Use Success or
// WithResponse for success. Arrays are copied; validation State remains borrowed.
type Details struct {
	// CorrelationID is echoed unchanged; transport normalization happens separately.
	CorrelationID concepts.UUID
	// Completion is server-side persistence evidence, never serialized in the envelope.
	Completion CompletionReport `json:"-"`
	operations *operationObservations
	// Authorized states the final authorization decision.
	Authorized bool
	// ValidationResults are retained findings after severity filtering.
	ValidationResults []validation.Result
	// ExceptionMessages are client-safe messages; nonempty means exception failure.
	ExceptionMessages []string
	// ExceptionStackTrace is empty outside explicit development exposure.
	ExceptionStackTrace string
	// AuthorizationFailureReason is safe client-visible denial feedback.
	AuthorizationFailureReason string
}

// Result is a finalized command outcome with computed flags and explicit response
// presence. Its zero value denies authorization. It is safe for concurrent reads
// if borrowed response and State values are not mutated. It is an output envelope,
// not a JSON decoder; use ordinary DTOs to consume remote responses.
type Result[R any] struct {
	details  Details
	response serialization.Optional[R]
}

// NewResult finalizes details and copies their arrays. Any failure discards response
// presence, including scalar zeros. Application response values remain borrowed.
func NewResult[R any](details Details, response serialization.Optional[R]) Result[R] {
	details.ValidationResults = wire.Findings(details.ValidationResults)
	details.ExceptionMessages = wire.Messages(details.ExceptionMessages)
	if details.operations != nil {
		observations := *details.operations
		observations.outcomes = append([]OperationOutcome(nil), observations.outcomes...)
		details.operations = &observations
	}
	result := Result[R]{details: details, response: response}
	if !result.IsSuccess() {
		result.response = serialization.Optional[R]{}
	}
	return result
}

// Success returns a successful command without a response.
func Success(id concepts.UUID) Result[NoResponse] {
	return NewResult(Details{CorrelationID: id, Authorized: true}, serialization.Optional[NoResponse]{})
}

// WithResponse returns success with an explicitly present response, including zero.
func WithResponse[R any](id concepts.UUID, response R) Result[R] {
	return NewResult(Details{CorrelationID: id, Authorized: true}, serialization.Some(response))
}

// Details returns copy-isolated arrays; application State remains borrowed.
func (r Result[R]) Details() Details {
	d := r.details
	d.ValidationResults = wire.Findings(d.ValidationResults)
	d.ExceptionMessages = wire.Messages(d.ExceptionMessages)
	return d
}

// Response distinguishes absence from a legitimate zero value. Nil payloads are
// omitted on the wire, even if present locally, matching C# null omission.
func (r Result[R]) Response() (R, bool) { return r.response.Value() }

// Completion returns persistence evidence, including after command/cleanup failure.
func (r Result[R]) Completion() CompletionReport { return r.details.Completion }

// IsAuthorized reports the final authorization decision.
func (r Result[R]) IsAuthorized() bool { return r.details.Authorized }

// IsValid reports whether the retained validation list is empty.
func (r Result[R]) IsValid() bool { return len(r.details.ValidationResults) == 0 }

// HasExceptions reports whether exceptionMessages is nonempty.
func (r Result[R]) HasExceptions() bool { return len(r.details.ExceptionMessages) > 0 }

// IsSuccess combines authorization, validity and absence of exceptions.
func (r Result[R]) IsSuccess() bool { return r.IsAuthorized() && r.IsValid() && !r.HasExceptions() }

// StatusCode selects 200, 403, 400 or 500 in Arc precedence order.
func (r Result[R]) StatusCode() int {
	return wire.StatusCode(r.IsSuccess(), r.IsAuthorized(), r.IsValid(), true)
}

// MarshalJSON emits the Arc CommandResult envelope with required arrays and strings.
func (r Result[R]) MarshalJSON() ([]byte, error) {
	return serialization.Marshal(r)
}

// MarshalJSONWith encodes nested values using the supplied Arc traversal. The
// callback is synchronous and is not retained; callers normally use MarshalJSON.
func (r Result[R]) MarshalJSONWith(encode func(any) ([]byte, error)) (body []byte, err error) {
	defer func() { err = withCompletionError(err, r.Completion()) }()
	var response json.RawMessage
	if value, present := r.Response(); present {
		var err error
		response, err = wire.Payload(value, encode)
		if err != nil {
			return nil, err
		}
	}
	findings, err := encode(wire.Findings(r.details.ValidationResults))
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		CorrelationID              concepts.UUID   `json:"correlationId"`
		IsSuccess                  bool            `json:"isSuccess"`
		IsAuthorized               bool            `json:"isAuthorized"`
		IsValid                    bool            `json:"isValid"`
		HasExceptions              bool            `json:"hasExceptions"`
		ValidationResults          json.RawMessage `json:"validationResults"`
		ExceptionMessages          []string        `json:"exceptionMessages"`
		ExceptionStackTrace        string          `json:"exceptionStackTrace"`
		AuthorizationFailureReason string          `json:"authorizationFailureReason"`
		Response                   json.RawMessage `json:"response,omitempty"`
	}{r.details.CorrelationID, r.IsSuccess(), r.IsAuthorized(), r.IsValid(), r.HasExceptions(), findings, wire.Messages(r.details.ExceptionMessages), r.details.ExceptionStackTrace, r.details.AuthorizationFailureReason, response})
}
