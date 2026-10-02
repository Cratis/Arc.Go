// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package correlation

import (
	"context"
	"errors"
	"strings"

	"github.com/cratis/arc.go/concepts"
)

// ID aliases the approved UUID value used by command/query results.
type ID = concepts.UUID

// DefaultHeader is the correlation header used at HTTP ingress.
const DefaultHeader = "X-Correlation-ID"

// ErrInvalidID identifies malformed, missing, or zero correlation input.
var ErrInvalidID = errors.New("invalid correlation ID")

type idKey struct{}

// Parse trims surrounding whitespace and accepts nonzero dashed UUIDs.
func Parse(text string) (ID, error) {
	id, err := concepts.ParseUUID(strings.TrimSpace(text))
	if err != nil || id.IsZero() {
		return ID{}, ErrInvalidID
	}
	return id, nil
}

// Normalize replaces invalid input with a new UUID, preserving generation failures.
func Normalize(text string) (ID, error) {
	if id, err := Parse(text); err == nil {
		return id, nil
	}
	return concepts.NewUUID()
}

// Resolve prefers valid supplied text, then a nonzero context ID, then generation.
func Resolve(ctx context.Context, text string) (ID, error) {
	if id, err := Parse(text); err == nil {
		return id, nil
	}
	if id := FromContext(ctx); !id.IsZero() {
		return id, nil
	}
	return concepts.NewUUID()
}

// WithID installs the ID; zero shadows an inherited ID. Correlation grants no authority.
func WithID(ctx context.Context, id ID) context.Context { return context.WithValue(ctx, idKey{}, id) }

// FromContext returns zero when absent and never generates an ID.
func FromContext(ctx context.Context) ID { id, _ := ctx.Value(idKey{}).(ID); return id }
