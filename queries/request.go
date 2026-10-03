// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"
	"encoding/json"
	"net/url"
)

// Request supplies raw or exact typed arguments and provider-neutral controls.
// Typed values are borrowed and do not inherit transport empty/null omission.
type Request struct {
	arguments  Arguments
	parameters Parameters
	typed      any
	hasTyped   bool
	wait       WaitOptions
}

// NewRequest constructs a raw request. Zero Arguments is valid and empty.
func NewRequest(a Arguments, p Parameters) Request { return Request{arguments: a, parameters: p} }

// RequestFor bypasses conversion with an exact typed argument model.
func RequestFor[A any](a A, p Parameters) Request {
	return Request{typed: a, hasTyped: true, parameters: p}
}

// Arguments returns raw presence metadata. Typed requests have no raw entries.
func (r Request) Arguments() Arguments { return r.arguments }

// Parameters returns request paging/sorting by value.
func (r Request) Parameters() Parameters { return r.parameters }

// PagingRequest is the QUERY JSON paging contract.
type PagingRequest struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"pageSize"`
}

// SortingRequest is the QUERY JSON sorting contract; nil direction defaults ascending.
type SortingRequest struct {
	Field     string  `json:"field"`
	Direction *string `json:"direction"`
}

// RequestEnvelope is the QUERY body contract. Unknown fields remain permissive.
type RequestEnvelope struct {
	Arguments map[string]json.RawMessage `json:"arguments"`
	Paging    *PagingRequest             `json:"paging"`
	Sorting   *SortingRequest            `json:"sorting"`
}

// ReaderInput is transport data after hosting has enforced request-size limits.
type ReaderInput struct {
	Query url.Values
	Body  []byte
}

// RequestReader normalizes a transport into Request without application activation.
type RequestReader interface {
	Method() string
	Read(context.Context, ReaderInput) (Request, error)
	ResponseCacheControl() string
}

// QueryStringRequestReader reads GET controls and raw arguments.
type QueryStringRequestReader struct{}

// Method reports GET.
func (QueryStringRequestReader) Method() string { return "GET" }

// ResponseCacheControl leaves caching policy to hosting for GET.
func (QueryStringRequestReader) ResponseCacheControl() string { return "" }

// Read checks cancellation and delegates to ReadGET.
func (QueryStringRequestReader) Read(ctx context.Context, in ReaderInput) (Request, error) {
	if ctx == nil {
		return Request{}, ErrMalformedRequest
	}
	if err := ctx.Err(); err != nil {
		return Request{}, err
	}
	return ReadGET(in.Query)
}

// BodyRequestReader reads QUERY; hosts must apply no-store on success and failure.
type BodyRequestReader struct{}

// Method reports QUERY.
func (BodyRequestReader) Method() string { return "QUERY" }

// ResponseCacheControl requires no-store on success and reader failure.
func (BodyRequestReader) ResponseCacheControl() string { return "no-store" }

// Read checks cancellation and delegates to ReadQUERY.
func (BodyRequestReader) Read(ctx context.Context, in ReaderInput) (Request, error) {
	if ctx == nil {
		return Request{}, ErrMalformedRequest
	}
	if err := ctx.Err(); err != nil {
		return Request{}, err
	}
	request, err := ReadQUERY(in.Body)
	if err != nil {
		return Request{}, err
	}
	controls, err := readWaitControls(in.Query)
	if err != nil {
		return Request{}, err
	}
	return request.WithWait(parseWait(controls)), nil
}
