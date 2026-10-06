// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts

import (
	"time"

	fconcepts "github.com/cratis/fundamentals.go/concepts"
)

// DateOnly is a Gregorian date in years 0001 through 9999, without a time zone.
// The zero value is 0001-01-01. JSON is the invariant yyyy-MM-dd string.
// It is an alias of the shared Fundamentals.Go type.
type DateOnly = fconcepts.DateOnly

// NewDateOnly validates a date; impossible dates are errors, not normalized.
func NewDateOnly(year int, month time.Month, day int) (DateOnly, error) {
	return fconcepts.NewDateOnly(year, month, day)
}

// ParseDateOnly accepts invariant yyyy-MM-dd, not culture-dependent dates.
func ParseDateOnly(text string) (DateOnly, error) { return fconcepts.ParseDateOnly(text) }
