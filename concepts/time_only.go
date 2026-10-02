// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const ticksPerSecond int64 = 10_000_000
const ticksPerDay int64 = 86400 * ticksPerSecond

// TimeOnly is a time of day with .NET's 100-nanosecond precision and no zone.
// Its zero value is midnight; JSON always includes seven fractional digits.
type TimeOnly struct{ ticks int64 }

// NewTimeOnly accepts ticks since midnight in [0, 864000000000).
func NewTimeOnly(ticks int64) (TimeOnly, error) {
	if ticks < 0 || ticks >= ticksPerDay {
		return TimeOnly{}, fmt.Errorf("TimeOnly ticks outside one day")
	}
	return TimeOnly{ticks}, nil
}

// ParseTimeOnly accepts HH:mm:ss with an optional one-to-seven digit fraction.
func ParseTimeOnly(text string) (TimeOnly, error) {
	ticks, err := parseClock(text)
	if err != nil {
		return TimeOnly{}, err
	}
	return NewTimeOnly(ticks)
}

// Ticks returns 100-nanosecond units since midnight.
func (t TimeOnly) Ticks() int64 { return t.ticks }

// String returns the .NET TimeOnly round-trip (O) representation.
func (t TimeOnly) String() string { return formatClock(uint64(t.ticks), true) }

// MarshalText implements encoding.TextMarshaler.
func (t TimeOnly) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText leaves t unchanged on invalid input.
func (t *TimeOnly) UnmarshalText(text []byte) error {
	v, err := ParseTimeOnly(string(text))
	if err != nil {
		return err
	}
	*t = v
	return nil
}

// MarshalJSON emits a JSON string.
func (t TimeOnly) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

// UnmarshalJSON rejects null and non-string values.
func (t *TimeOnly) UnmarshalJSON(data []byte) error { return unmarshalString(data, t.UnmarshalText) }

func parseClock(text string) (int64, error) {
	clock, fraction, hasFraction := strings.Cut(text, ".")
	if len(clock) != 8 || clock[2] != ':' || clock[5] != ':' {
		return 0, fmt.Errorf("time requires HH:mm:ss")
	}
	h, e1 := decimal(clock[:2])
	m, e2 := decimal(clock[3:5])
	s, e3 := decimal(clock[6:])
	if e1 != nil || e2 != nil || e3 != nil || h > 23 || m > 59 || s > 59 {
		return 0, fmt.Errorf("invalid time of day")
	}
	var f uint64
	if hasFraction {
		if len(fraction) == 0 || len(fraction) > 7 {
			return 0, fmt.Errorf("fraction requires one to seven digits")
		}
		var err error
		f, err = decimal(fraction + strings.Repeat("0", 7-len(fraction)))
		if err != nil {
			return 0, err
		}
	}
	return int64(h*3600+m*60+s)*ticksPerSecond + int64(f), nil
}

func decimal(text string) (uint64, error) {
	if text == "" {
		return 0, fmt.Errorf("empty decimal")
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid decimal")
		}
	}
	return strconv.ParseUint(text, 10, 64)
}

func formatClock(ticks uint64, alwaysFraction bool) string {
	seconds := ticks / uint64(ticksPerSecond)
	text := fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	fraction := ticks % uint64(ticksPerSecond)
	if alwaysFraction || fraction != 0 {
		text += fmt.Sprintf(".%07d", fraction)
	}
	return text
}
