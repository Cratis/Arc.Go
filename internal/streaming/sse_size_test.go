// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import (
	"math"
	"testing"
)

func TestSSEFrameSizeValidatesBeforeAddingOverhead(t *testing.T) {
	for _, tc := range []struct {
		name          string
		payload, want int
		invalid       bool
	}{
		{name: "empty", payload: 0, want: 8},
		{name: "default response limit", payload: (16 << 20) - 8, want: 16 << 20},
		{name: "allocation ceiling", payload: math.MaxInt - 8, want: math.MaxInt},
		{name: "one byte over ceiling", payload: math.MaxInt - 7, invalid: true},
		{name: "maximum integer", payload: math.MaxInt, invalid: true},
		{name: "negative", payload: -1, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			size, err := sseFrameSize(tc.payload)
			if (err != nil) != tc.invalid || size != tc.want {
				t.Fatalf("size(%d) = %d, %v; want %d, invalid=%v", tc.payload, size, err, tc.want, tc.invalid)
			}
		})
	}
}
