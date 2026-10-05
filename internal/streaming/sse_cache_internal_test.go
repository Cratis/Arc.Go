// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import "testing"

func TestCacheControlHasNoStoreMatchesDirectiveTokensOnly(t *testing.T) {
	for value, want := range map[string]bool{
		"":                        false,
		"no-store":                true,
		"No-Store":                true,
		" no-store ":              true,
		"no-store, private":       true,
		"private, no-store":       true,
		"private":                 false,
		"private, max-age=600":    false,
		"x-no-store-ish":          false,
		"no-store-ish, private":   false,
		"max-age=600,no-store":    true,
		"private, x-no-store-ish": false,
	} {
		if got := cacheControlHasNoStore(value); got != want {
			t.Errorf("cacheControlHasNoStore(%q) = %v, want %v", value, got, want)
		}
	}
}
