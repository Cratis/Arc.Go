// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package correlation

import (
	"context"
	"errors"
	"strings"

	"github.com/cratis/arc.go/concepts"
	fcorrelation "github.com/cratis/fundamentals.go/correlation"
)

// ID aliases the approved UUID value used by command/query results.
type ID = concepts.UUID

// DefaultHeader is the correlation header used at HTTP ingress.
const DefaultHeader = "X-Correlation-ID"

// ErrInvalidID identifies malformed, missing, or zero correlation input.
var ErrInvalidID = errors.New("invalid correlation ID")

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

// WithID installs the ID using Fundamentals.Go's shared context key; zero shadows
// an inherited ID. Correlation grants no authority. It panics if ctx is nil.
func WithID(ctx context.Context, id ID) context.Context { return fcorrelation.WithID(ctx, id) }

// FromContext reads Fundamentals.Go's shared context key, returns zero when absent,
// and never generates an ID. It panics if ctx is nil.
func FromContext(ctx context.Context) ID { return fcorrelation.FromContext(ctx) }
