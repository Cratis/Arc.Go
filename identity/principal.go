// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package identity

import "slices"

// Claim is one immutable claim pair. Repeated types and order are preserved.
type Claim struct {
	// Type is the exact, case-sensitive claim type.
	Type string
	// Value is the claim value.
	Value string
}

// PrincipalData is trusted application input, copied by NewPrincipal.
type PrincipalData struct {
	// ID is the verified subject supplied by the adapter.
	ID string
	// Name is the display name.
	Name string
	// AuthenticationType determines authentication when nonempty.
	AuthenticationType string
	// Roles are explicit, case-sensitive roles, not inferred from Claims.
	Roles []string
	// Claims preserve input order and duplicate types.
	Claims []Claim
}

// Principal is an immutable snapshot safe for concurrent use; zero is anonymous.
// Construction does not authenticate credentials.
type Principal struct{ data PrincipalData }

// NewPrincipal copies trusted input; only verified adapters/application code should call it.
// An unauthenticated principal's ID and claims are not trusted identity data.
// Authorization policies receive an empty principal for anonymous callers.
func NewPrincipal(data PrincipalData) Principal {
	data.Roles = slices.Clone(data.Roles)
	data.Claims = slices.Clone(data.Claims)
	return Principal{data: data}
}

// ID returns the explicitly supplied subject.
func (p Principal) ID() string { return p.data.ID }

// Name returns the display name.
func (p Principal) Name() string { return p.data.Name }

// AuthenticationType returns the trusted authentication type.
func (p Principal) AuthenticationType() string { return p.data.AuthenticationType }

// IsAuthenticated reports whether AuthenticationType is nonempty.
func (p Principal) IsAuthenticated() bool { return p.data.AuthenticationType != "" }

// Roles returns an independent copy of the explicit roles.
func (p Principal) Roles() []string { return slices.Clone(p.data.Roles) }

// Claims returns an independent copy of the ordered claims.
func (p Principal) Claims() []Claim { return slices.Clone(p.data.Claims) }

// Claim returns the first exact, case-sensitive matching claim.
func (p Principal) Claim(claimType string) (string, bool) {
	for _, claim := range p.data.Claims {
		if claim.Type == claimType {
			return claim.Value, true
		}
	}
	return "", false
}

// HasRole requires authentication and an exact, case-sensitive explicit role.
func (p Principal) HasRole(role string) bool {
	return p.IsAuthenticated() && slices.Contains(p.data.Roles, role)
}

// Equal compares all fields and ordered slice content.
func (p Principal) Equal(other Principal) bool {
	return p.ID() == other.ID() && p.Name() == other.Name() && p.AuthenticationType() == other.AuthenticationType() && slices.Equal(p.data.Roles, other.data.Roles) && slices.Equal(p.data.Claims, other.data.Claims)
}

// System creates an authenticated actor, not an authorization bypass.
func System(roles ...string) Principal {
	claims := []Claim{
		{Type: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/nameidentifier", Value: "[System]"},
		{Type: "sub", Value: "[System]"},
		{Type: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name", Value: "[System]"},
	}
	for _, role := range roles {
		claims = append(claims, Claim{Type: "http://schemas.microsoft.com/ws/2008/06/identity/claims/role", Value: role})
	}
	return NewPrincipal(PrincipalData{ID: "[System]", Name: "[System]", AuthenticationType: "System", Roles: roles, Claims: claims})
}
