// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package logging sanitizes untrusted values before handing them to host loggers.
package logging

import (
	"strconv"
	"strings"
	"unicode"
)

// String escapes control characters, including CR and LF, even when the host's
// slog handler emits attribute values without quoting. Printable text is unchanged.
func String(value string) string {
	value = strings.ReplaceAll(value, "\r", `\r`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	var escaped strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			quoted := strconv.QuoteRune(r)
			escaped.WriteString(quoted[1 : len(quoted)-1])
		} else {
			escaped.WriteRune(r)
		}
	}
	return escaped.String()
}
