// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package fixture_test

import (
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/recipes/internal/fixture"
)

// The bare Arc handler is the baseline every framework mount must match.
func TestArcServedDirectlySatisfiesTheMountContract(t *testing.T) {
	f := fixture.New(t, arc.Options{})
	fixture.VerifyMount(t, f, fixture.Serve(t, f.App))
}
