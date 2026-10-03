// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package metadata

import (
	"fmt"
	"slices"
	"strings"
)

// GeneratedContractMismatchError reports drift between generated proxies and the
// complete runtime catalog. Expected and Actual are independently owned snapshots.
type GeneratedContractMismatchError struct {
	Profile  string
	Identity string
	Expected []Endpoint
	Actual   []Endpoint
}

func (e *GeneratedContractMismatchError) Error() string {
	return fmt.Sprintf("generated contract %q for %q: expected endpoints %v; actual endpoints %v", e.Profile, e.Identity, e.Expected, e.Actual)
}

// VerifyGeneratedEndpoints compares only identities owned by expected. Call it
// after Resolve on the complete catalog, including manual and excluded artifacts.
// Extra methods, absent artifacts and validation-path changes are mismatches.
// Inputs are borrowed and never modified. Profile is diagnostic provenance, not
// a replacement for comparing endpoints. Empty or malformed expectations fail.
func VerifyGeneratedEndpoints(profile string, expected, actual []Endpoint) error {
	if strings.TrimSpace(profile) == "" || len(expected) == 0 {
		return fmt.Errorf("generated contract requires a profile and owned endpoints")
	}
	owned := map[string][]Endpoint{}
	seen := map[Endpoint]bool{}
	for _, endpoint := range expected {
		if endpoint.Identity == "" || endpoint.Path == "" || (endpoint.Method != "POST" && endpoint.Method != "GET" && endpoint.Method != "QUERY") || seen[endpoint] {
			return fmt.Errorf("generated contract %q: invalid or duplicate expected endpoint %v", profile, endpoint)
		}
		seen[endpoint] = true
		owned[endpoint.Identity] = append(owned[endpoint.Identity], endpoint)
	}
	identities := make([]string, 0, len(owned))
	for identity := range owned {
		identities = append(identities, identity)
	}
	slices.Sort(identities)
	for _, identity := range identities {
		want := owned[identity]
		var got []Endpoint
		for _, endpoint := range actual {
			if endpoint.Identity == identity {
				got = append(got, endpoint)
			}
		}
		sortEndpoints(want)
		sortEndpoints(got)
		if !slices.Equal(want, got) {
			return &GeneratedContractMismatchError{Profile: profile, Identity: identity, Expected: want, Actual: got}
		}
	}
	return nil
}

func sortEndpoints(endpoints []Endpoint) {
	slices.SortFunc(endpoints, func(a, b Endpoint) int {
		if comparison := strings.Compare(a.Method, b.Method); comparison != 0 {
			return comparison
		}
		if comparison := strings.Compare(a.Path, b.Path); comparison != 0 {
			return comparison
		}
		if a.ValidateOnly == b.ValidateOnly {
			return 0
		}
		if a.ValidateOnly {
			return 1
		}
		return -1
	})
}
