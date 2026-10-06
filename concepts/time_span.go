// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts

import fconcepts "github.com/cratis/fundamentals.go/concepts"

// TimeSpan is a signed .NET duration in 100-nanosecond ticks. Unlike time.Duration,
// it preserves the entire int64 tick range. Zero is a duration of zero.
// It is an alias of the shared Fundamentals.Go type.
type TimeSpan = fconcepts.TimeSpan

// ParseTimeSpan accepts [-][d.]HH:mm:ss[.fffffff] in invariant constant format.
func ParseTimeSpan(text string) (TimeSpan, error) { return fconcepts.ParseTimeSpan(text) }
