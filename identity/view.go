// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package identity

import (
	"encoding/json"

	"github.com/cratis/arc.go/serialization"
)

// View is caller-owned display data, not trusted context metadata. Details is
// borrowed application data; do not mutate it during encoding. Roles emits []
// rather than null, and details is emitted unwrapped (including null).
type View[T any] struct {
	// ID is the display identifier.
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// IsAuthenticated describes the captured trusted principal.
	IsAuthenticated bool `json:"isAuthenticated"`
	// IsAuthorized describes the details provider's verdict, not operation admission.
	IsAuthorized bool `json:"isAuthorized"`
	// Roles is copied from the trusted principal, never inferred from Details.
	Roles []string `json:"roles"`
	// Details is application-owned display data.
	Details T `json:"details"`
}

// MarshalJSON encodes the identity view using Arc's bounded serialization.
func (v View[T]) MarshalJSON() ([]byte, error) { return serialization.Marshal(v) }

// MarshalJSONWith encodes nested details with the supplied Arc traversal. The
// synchronous callback is borrowed and not retained; normally use MarshalJSON.
func (v View[T]) MarshalJSONWith(encode func(any) ([]byte, error)) ([]byte, error) {
	details, err := encode(v.Details)
	if err != nil {
		return nil, err
	}
	roles := v.Roles
	if roles == nil {
		roles = []string{}
	}
	return json.Marshal(struct {
		ID              string          `json:"id"`
		Name            string          `json:"name"`
		IsAuthenticated bool            `json:"isAuthenticated"`
		IsAuthorized    bool            `json:"isAuthorized"`
		Roles           []string        `json:"roles"`
		Details         json.RawMessage `json:"details"`
	}{v.ID, v.Name, v.IsAuthenticated, v.IsAuthorized, roles, details})
}
