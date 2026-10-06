// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package logging sanitizes untrusted values before handing them to host loggers.
package logging

import (
	"strings"
	"unicode"
)

// String removes control characters, including CR and LF, even when the host's
// slog handler emits attribute values without quoting. Printable text is unchanged.
func String(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, value)
}
