// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package logging_test

import (
	"testing"

	"github.com/cratis/arc.go/internal/logging"
)

func TestStringEscapesControlCharactersAndPreservesPrintableText(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"printable", `query "café" failed: /path`, `query "café" failed: /path`},
		{"line injection", "first\r\nFORGED\nlast", `first\r\nFORGED\nlast`},
		{"controls", "a\x00\t\x1b\x7f\u0085\u2028\u2029b", `a\x00\t\x1b\x7f\u0085\u2028\u2029b`},
		{"visible escapes", `first\r\nlast\x1b`, `first\\r\\nlast\\x1b`},
		{"literal newline escape", `a\nb`, `a\\nb`},
		{"real newline", "a\nb", `a\nb`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := logging.String(tc.input); got != tc.want {
				t.Fatalf("String(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
