// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authentication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cratis/arc.go/identity"
)

// MicrosoftIdentityProviderClaim is Arc-authored forwarded provider metadata.
const MicrosoftIdentityProviderClaim = "urn:cratis:arc:identity:provider"

// MicrosoftIdentityOptions configures the unsigned forwarded-principal adapter.
// TrustForwardedIdentityHeaders requires an isolating, credential-validating
// ingress which strips caller headers, writes verified replacements and prevents
// direct backend access. Base64 is not authentication proof. Zero trust ignores
// all headers. MaxPrincipalBytes zero selects 64 KiB; negative is invalid.
type MicrosoftIdentityOptions struct {
	TrustForwardedIdentityHeaders bool
	MaxPrincipalBytes             int
}

// MicrosoftIdentityPlatform explicitly constructs a borrowed concurrent-safe
// handler; it is never registered automatically and does not validate JWTs.
func MicrosoftIdentityPlatform(options MicrosoftIdentityOptions) (Handler, error) {
	if options.MaxPrincipalBytes < 0 {
		return nil, ErrInvalidHandler
	}
	if options.MaxPrincipalBytes == 0 {
		options.MaxPrincipalBytes = 64 << 10
	}
	return HandlerFunc(func(ctx context.Context, r *http.Request) (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if !options.TrustForwardedIdentityHeaders {
			return Anonymous(), nil
		}
		names := []string{"x-ms-client-principal-id", "x-ms-client-principal-name", "x-ms-client-principal"}
		values := make([]string, 3)
		for i, name := range names {
			var entries []string
			for key, v := range r.Header {
				if strings.EqualFold(key, name) {
					entries = append(entries, v...)
				}
			}
			if len(entries) == 0 {
				return Anonymous(), nil
			}
			if len(entries) != 1 {
				return Failed("ambiguous forwarded identity"), nil
			}
			values[i] = entries[0]
		}
		invalid := func() (Result, error) { return Failed("invalid forwarded identity"), nil }
		if len(values[2]) > base64.StdEncoding.EncodedLen(options.MaxPrincipalBytes) {
			return invalid()
		}
		decoded, err := base64.StdEncoding.DecodeString(values[2])
		if err != nil || len(decoded) > options.MaxPrincipalBytes {
			return invalid()
		}
		var principal *identity.ClientPrincipal
		if err := json.Unmarshal(decoded, &principal); err != nil || principal == nil {
			return invalid()
		}
		claims := []identity.Claim{}
		const subject = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/nameidentifier"
		for _, claim := range principal.Claims {
			if claim.Type == subject || claim.Type == "sub" || strings.EqualFold(claim.Type, MicrosoftIdentityProviderClaim) {
				continue
			}
			claims = append(claims, identity.Claim{Type: claim.Type, Value: claim.Value})
		}
		claims = append(claims, identity.Claim{Type: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name", Value: principal.UserDetails}, identity.Claim{Type: subject, Value: values[0]}, identity.Claim{Type: "sub", Value: values[0]})
		if strings.TrimSpace(principal.IdentityProvider) != "" {
			claims = append(claims, identity.Claim{Type: MicrosoftIdentityProviderClaim, Value: principal.IdentityProvider})
		}
		for _, role := range principal.UserRoles {
			claims = append(claims, identity.Claim{Type: "http://schemas.microsoft.com/ws/2008/06/identity/claims/role", Value: role})
		}
		return Authenticated(identity.NewPrincipal(identity.PrincipalData{ID: values[0], Name: principal.UserDetails, AuthenticationType: "MicrosoftIdentityPlatformAuthenticationHandler", Roles: principal.UserRoles, Claims: claims}))
	}), nil
}
