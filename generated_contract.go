// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"fmt"
	"slices"

	"github.com/cratis/arc.go/metadata"
)

type generatedContract struct {
	profile   string
	endpoints []metadata.Endpoint
}

// ExpectGeneratedEndpoints records the complete endpoint expectations for owned
// artifact identities. Generated adapters call this before Build; proxy-only
// consumers must register the same expectations explicitly. Inputs are copied.
// Different registrations may share a profile fingerprint, but not identities.
// Build compares expectations with the final complete catalog before activation.
func (b *Builder) ExpectGeneratedEndpoints(profile string, endpoints []metadata.Endpoint) error {
	if b == nil {
		return fmt.Errorf("generated contract requires a builder")
	}
	if b.attempted {
		return ErrFrozen
	}
	// Comparing a contract to itself validates its nonempty shape without another
	// route algorithm; actual endpoints are resolved once by Build.
	if err := metadata.VerifyGeneratedEndpoints(profile, endpoints, endpoints); err != nil {
		return err
	}
	for _, old := range b.generatedContracts {
		for _, prior := range old.endpoints {
			for _, endpoint := range endpoints {
				if prior.Identity == endpoint.Identity {
					return fmt.Errorf("generated contract identity %q already owned by profile %q", endpoint.Identity, old.profile)
				}
			}
		}
	}
	b.generatedContracts = append(b.generatedContracts, generatedContract{profile, slices.Clone(endpoints)})
	return nil
}
