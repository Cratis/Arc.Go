// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package clientfixture

import (
	"fmt"
	"io/fs"
)

// NewBrowser builds the generated fixture with Arc's cookie-based anonymous hub
// ownership and same-origin policy, without the constant owner used by NewMode.
// It serves the borrowed, trusted assets at /fixture/browser/ on the same host.
// Assets must be non-nil and remain readable for the host's lifetime; files must
// implement io.Seeker, as required by http.FileServerFS. The caller owns App.Serve
// and its join. This unauthenticated control fixture is for loopback tests only,
// not production hosting. It adds neither a CORS policy nor a credential override.
func NewBrowser(assets fs.FS) (*Fixture, error) {
	if assets == nil {
		return nil, fmt.Errorf("browser fixture requires an asset filesystem")
	}
	return newFixture(true, assets)
}
