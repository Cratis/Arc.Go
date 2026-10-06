// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package identity

// User is development display data, never authentication evidence.
type User struct {
	MicrosoftIdentity ClientPrincipal `json:"microsoftIdentity"`
	Details           any             `json:"details,omitempty"`
}

// ClientPrincipal is the Microsoft identity platform discovery DTO. Only an
// explicitly trusted authentication adapter may turn it into a Principal.
type ClientPrincipal struct {
	IdentityProvider string                 `json:"identityProvider"`
	UserID           string                 `json:"userId"`
	UserDetails      string                 `json:"userDetails"`
	UserRoles        []string               `json:"userRoles"`
	Claims           []ClientPrincipalClaim `json:"claims"`
}

// ClientPrincipalClaim preserves the forwarded typ/val contract.
type ClientPrincipalClaim struct {
	Type  string `json:"typ"`
	Value string `json:"val"`
}
