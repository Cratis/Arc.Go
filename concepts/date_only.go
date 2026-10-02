// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts

import (
	"encoding/json"
	"fmt"
	"time"
)

// DateOnly is a Gregorian date in years 0001 through 9999, without a time zone.
// The zero value is 0001-01-01. JSON is the invariant yyyy-MM-dd string.
type DateOnly struct {
	year  uint16
	month uint8
	day   uint8
}

// NewDateOnly validates a date; impossible dates are errors, not normalized.
func NewDateOnly(year int, month time.Month, day int) (DateOnly, error) {
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if year < 1 || year > 9999 || t.Year() != year || t.Month() != month || t.Day() != day {
		return DateOnly{}, fmt.Errorf("invalid Gregorian date")
	}
	return DateOnly{uint16(year - 1), uint8(month - 1), uint8(day - 1)}, nil
}

// ParseDateOnly accepts invariant yyyy-MM-dd, not culture-dependent dates.
func ParseDateOnly(text string) (DateOnly, error) {
	t, err := time.Parse(time.DateOnly, text)
	if err != nil {
		return DateOnly{}, fmt.Errorf("parse DateOnly: %w", err)
	}
	return NewDateOnly(t.Year(), t.Month(), t.Day())
}

// Date returns the Gregorian year, month and day.
func (d DateOnly) Date() (int, time.Month, int) {
	return int(d.year) + 1, time.Month(d.month) + 1, int(d.day) + 1
}

// String returns invariant yyyy-MM-dd.
func (d DateOnly) String() string {
	y, m, day := d.Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, m, day)
}

// MarshalText implements encoding.TextMarshaler.
func (d DateOnly) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText leaves d unchanged on invalid input.
func (d *DateOnly) UnmarshalText(text []byte) error {
	value, err := ParseDateOnly(string(text))
	if err != nil {
		return err
	}
	*d = value
	return nil
}

// MarshalJSON emits the date as a string.
func (d DateOnly) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON rejects null and non-string values.
func (d *DateOnly) UnmarshalJSON(data []byte) error { return unmarshalString(data, d.UnmarshalText) }
