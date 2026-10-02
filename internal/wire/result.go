// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package wire contains shared result-encoding mechanics, not public envelopes.
package wire

import (
	"encoding/json"
	"net/http"

	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// Findings returns a copy with non-nil array semantics.
func Findings(values []validation.Result) []validation.Result {
	result := make([]validation.Result, len(values))
	for i, value := range values {
		result[i] = value.Clone()
	}
	return result
}

// Messages returns an independent non-nil array.
func Messages(values []string) []string { return append([]string{}, values...) }

// Payload omits null payloads without treating scalar zero as absent.
func Payload(value any) (json.RawMessage, error) {
	data, err := serialization.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(data) == "null" {
		return nil, nil
	}
	return data, nil
}

// StatusCode implements EndpointRouteHelper precedence, not ingress exceptions.
func StatusCode(success, authorized, valid, ready bool) int {
	switch {
	case success:
		return http.StatusOK
	case !authorized:
		return http.StatusForbidden
	case !valid:
		return http.StatusBadRequest
	case !ready:
		return http.StatusAccepted
	default:
		return http.StatusInternalServerError
	}
}
