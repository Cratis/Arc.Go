// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts

import fconcepts "github.com/cratis/fundamentals.go/concepts"

// TimeOnly is a time of day with .NET's 100-nanosecond precision and no zone.
// Its zero value is midnight; JSON always includes seven fractional digits.
// It is an alias of the shared Fundamentals.Go type.
type TimeOnly = fconcepts.TimeOnly

// NewTimeOnly accepts ticks since midnight in [0, 864000000000).
func NewTimeOnly(ticks int64) (TimeOnly, error) { return fconcepts.NewTimeOnly(ticks) }

// ParseTimeOnly accepts HH:mm:ss with an optional one-to-seven digit fraction.
func ParseTimeOnly(text string) (TimeOnly, error) { return fconcepts.ParseTimeOnly(text) }
