// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package jwtauth verifies bearer JSON Web Tokens with golang-jwt v5 and
// supplies the verified subject to Arc as a trusted principal.
package jwtauth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/identity"
)

// recipe:start jwt-handler

// Claims are the token claims this recipe maps onto an Arc principal.
type Claims struct {
	jwt.RegisteredClaims
	Name     string   `json:"name,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	TokenUse string   `json:"token_use"`
}

// Validate requires this issuer's access-token marker; ID tokens are not API
// credentials. Adapt this check to your issuer's documented token profile.
func (c Claims) Validate() error {
	if c.TokenUse != "access" {
		return errors.New("not an access token")
	}
	return nil
}

// Bearer returns an Arc authentication handler for "Authorization: Bearer"
// tokens. Requests without that scheme stay anonymous for later handlers and
// authorization; a bearer token that does not verify is a terminal 401.
// Supply jwt.WithValidMethods, issuer, audience and expiry options: the
// parser accepts whatever the options do not forbid.
func Bearer(key jwt.Keyfunc, options ...jwt.ParserOption) authentication.Handler {
	parser := jwt.NewParser(options...)
	return authentication.HandlerFunc(func(ctx context.Context, r *http.Request) (authentication.Result, error) {
		if err := ctx.Err(); err != nil {
			return authentication.Result{}, err
		}
		headers := r.Header.Values("Authorization")
		if len(headers) == 0 {
			return authentication.Anonymous(), nil
		}
		if len(headers) != 1 {
			return authentication.Failed("ambiguous authorization header"), nil
		}
		parts := strings.Fields(headers[0])
		if len(parts) == 0 || !strings.EqualFold(parts[0], "Bearer") {
			return authentication.Anonymous(), nil
		}
		if len(parts) != 2 {
			return authentication.Failed("invalid bearer token"), nil
		}
		claims := &Claims{}
		if _, err := parser.ParseWithClaims(parts[1], claims, key); err != nil {
			// Reasons stay local; never echo token text or parser details.
			if errors.Is(err, jwt.ErrTokenExpired) {
				return authentication.Failed("expired bearer token"), nil
			}
			return authentication.Failed("invalid bearer token"), nil
		}
		if err := ctx.Err(); err != nil {
			return authentication.Result{}, err
		}
		if claims.Subject == "" {
			return authentication.Failed("bearer token has no subject"), nil
		}
		return authentication.Authenticated(identity.NewPrincipal(identity.PrincipalData{
			ID:                 claims.Subject,
			Name:               claims.Name,
			AuthenticationType: "Bearer",
			Roles:              claims.Roles,
			Claims:             []identity.Claim{{Type: "iss", Value: claims.Issuer}},
		}))
	})
}

// recipe:end
