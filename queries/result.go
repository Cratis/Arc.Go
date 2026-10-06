// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package queries registers read-model-owned typed snapshots, binds caller inputs,
// and stages authorized execution, optional rendering and ordered interception.
// Pipelines own operation scopes and observable stream handles, not persistence
// or transactions. Application-owned subjects remain borrowed.
// Runtime method discovery and reflective invocation are not provided. Existing
// outcome constructors remain independent of pipeline failure finalization.
package queries

import (
	"encoding/json"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/internal/wire"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// Details configures an outcome. Zero is unauthorized and not ready. A ready null
// result is distinct from a pending query. Arrays are copied; State is borrowed.
type Details struct {
	// CorrelationID is echoed unchanged.
	CorrelationID concepts.UUID
	// Ready distinguishes no emission from an emitted null or zero.
	Ready bool
	// Authorized states the final authorization decision.
	Authorized bool
	// ValidationResults are retained findings.
	ValidationResults []validation.Result
	// ExceptionMessages contains safe client-visible errors.
	ExceptionMessages []string
	// ExceptionStackTrace is empty outside explicit development exposure.
	ExceptionStackTrace string
	// Paging is always included; its zero value means not paged.
	Paging PagingInfo
	// ChangeSet optionally carries an observable delta; nil is omitted.
	ChangeSet *ChangeSet
}

// Result is an output-only query envelope. Use NewResult, Success or NotReady.
// It is safe for concurrent reads if borrowed data and State are not mutated.
type Result[T any] struct {
	details Details
	data    serialization.Optional[T]
}

// NewResult copies details' arrays and borrows data. Like C#, readiness is independent
// of data presence. Change-set slices are copied, but their items remain borrowed.
func NewResult[T any](details Details, data serialization.Optional[T]) Result[T] {
	details.ValidationResults = wire.Findings(details.ValidationResults)
	details.ExceptionMessages = wire.Messages(details.ExceptionMessages)
	details.ChangeSet = cloneChanges(details.ChangeSet)
	return Result[T]{details, data}
}

// Success returns a ready, authorized query with data (including a present nil).
func Success[T any](id concepts.UUID, data T) Result[T] {
	return NewResult(Details{CorrelationID: id, Authorized: true, Ready: true}, serialization.Some(data))
}

// NotReady returns an authorized query that has not emitted yet (HTTP 202).
func NotReady[T any](id concepts.UUID) Result[T] {
	return NewResult(Details{CorrelationID: id, Authorized: true}, serialization.Optional[T]{})
}

// Details returns isolated arrays; validation State remains borrowed.
func (r Result[T]) Details() Details {
	d := r.details
	d.ValidationResults = wire.Findings(d.ValidationResults)
	d.ExceptionMessages = wire.Messages(d.ExceptionMessages)
	d.ChangeSet = cloneChanges(d.ChangeSet)
	return d
}

// Data reports local value presence; JSON omits a nil payload even when present.
func (r Result[T]) Data() (T, bool) { return r.data.Value() }

// IsReady reports whether the query has produced its first result.
func (r Result[T]) IsReady() bool { return r.details.Ready }

// IsAuthorized reports the authorization decision.
func (r Result[T]) IsAuthorized() bool { return r.details.Authorized }

// IsValid reports whether there are no validation findings, regardless of severity.
func (r Result[T]) IsValid() bool { return len(r.details.ValidationResults) == 0 }

// HasExceptions reports nonempty exceptionMessages.
func (r Result[T]) HasExceptions() bool { return len(r.details.ExceptionMessages) > 0 }

// IsSuccess additionally requires readiness.
func (r Result[T]) IsSuccess() bool {
	return r.IsReady() && r.IsAuthorized() && r.IsValid() && !r.HasExceptions()
}

// StatusCode selects 200, 403, 400, 202 or 500 in Arc precedence order.
// Ingress failures (401), malformed QUERY readers (400) and waits (408) override this
// selection at the future transport boundary; they are not ordinary outcomes.
func (r Result[T]) StatusCode() int {
	return wire.StatusCode(r.IsSuccess(), r.IsAuthorized(), r.IsValid(), r.IsReady())
}

// MarshalJSON emits the Arc QueryResult envelope. It never emits an authorization
// failure reason; that property belongs only to commands.
func (r Result[T]) MarshalJSON() ([]byte, error) {
	return serialization.Marshal(r)
}

// MarshalJSONWith encodes nested values using the supplied Arc traversal. The
// callback is synchronous and is not retained; callers normally use MarshalJSON.
func (r Result[T]) MarshalJSONWith(encode func(any) ([]byte, error)) ([]byte, error) {
	var data json.RawMessage
	if value, present := r.Data(); present {
		var err error
		data, err = wire.Payload(value, encode)
		if err != nil {
			return nil, err
		}
	}
	findings, err := encode(wire.Findings(r.details.ValidationResults))
	if err != nil {
		return nil, err
	}
	changes, err := wire.Payload(r.details.ChangeSet, encode)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Paging              PagingInfo      `json:"paging"`
		CorrelationID       concepts.UUID   `json:"correlationId"`
		Data                json.RawMessage `json:"data,omitempty"`
		IsSuccess           bool            `json:"isSuccess"`
		IsReady             bool            `json:"isReady"`
		IsAuthorized        bool            `json:"isAuthorized"`
		IsValid             bool            `json:"isValid"`
		HasExceptions       bool            `json:"hasExceptions"`
		ValidationResults   json.RawMessage `json:"validationResults"`
		ExceptionMessages   []string        `json:"exceptionMessages"`
		ExceptionStackTrace string          `json:"exceptionStackTrace"`
		ChangeSet           json.RawMessage `json:"changeSet,omitempty"`
	}{r.details.Paging, r.details.CorrelationID, data, r.IsSuccess(), r.IsReady(), r.IsAuthorized(), r.IsValid(), r.HasExceptions(), findings, wire.Messages(r.details.ExceptionMessages), r.details.ExceptionStackTrace, changes})
}
