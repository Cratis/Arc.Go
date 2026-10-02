// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// TimeSpan is a signed .NET duration in 100-nanosecond ticks. Unlike time.Duration,
// it preserves the entire int64 tick range. Zero is a duration of zero.
type TimeSpan int64

// ParseTimeSpan accepts [-][d.]HH:mm:ss[.fffffff] in invariant constant format.
func ParseTimeSpan(text string) (TimeSpan, error) {
	negative := strings.HasPrefix(text, "-")
	if negative {
		text = text[1:]
	}
	var days uint64
	colon := strings.IndexByte(text, ':')
	dot := strings.IndexByte(text, '.')
	if dot >= 0 && colon > dot {
		var err error
		days, err = decimal(text[:dot])
		if err != nil {
			return 0, err
		}
		text = text[dot+1:]
	}
	clock, err := parseClock(text)
	if err != nil {
		return 0, err
	}
	limit := uint64(math.MaxInt64)
	if negative {
		limit++
	}
	if days > limit/uint64(ticksPerDay) {
		return 0, fmt.Errorf("TimeSpan overflow")
	}
	total := days*uint64(ticksPerDay) + uint64(clock)
	if total > limit {
		return 0, fmt.Errorf("TimeSpan overflow")
	}
	if negative {
		return TimeSpan(-int64(total)), nil
	}
	return TimeSpan(total), nil
}

// Ticks returns the signed number of 100-nanosecond units.
func (t TimeSpan) Ticks() int64 { return int64(t) }

// String returns the .NET TimeSpan invariant constant (c) representation.
func (t TimeSpan) String() string {
	magnitude := uint64(t)
	prefix := ""
	if t < 0 {
		prefix = "-"
		magnitude = uint64(-(t + 1)) + 1
	}
	days := magnitude / uint64(ticksPerDay)
	if days > 0 {
		prefix += fmt.Sprintf("%d.", days)
	}
	return prefix + formatClock(magnitude%uint64(ticksPerDay), false)
}

// MarshalText implements encoding.TextMarshaler.
func (t TimeSpan) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText leaves t unchanged on invalid input.
func (t *TimeSpan) UnmarshalText(text []byte) error {
	v, err := ParseTimeSpan(string(text))
	if err != nil {
		return err
	}
	*t = v
	return nil
}

// MarshalJSON emits a JSON string, not a Go duration or nanosecond count.
func (t TimeSpan) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

// UnmarshalJSON rejects null and non-string values.
func (t *TimeSpan) UnmarshalJSON(data []byte) error { return unmarshalString(data, t.UnmarshalText) }
