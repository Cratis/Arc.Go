// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package jwtauth_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/recipes/internal/fixture"
	"github.com/cratis/arc.go/recipes/jwtauth"
)

const (
	issuer   = "https://issuer.example"
	audience = "arc-recipes"
	mePath   = "/api/me"
)

var signingKey = []byte("recipe-test-only-hmac-key-0123456789abcdef")

type profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func registerMe(builder *arc.Builder) error {
	return queries.Register[profile](builder, "Me", queries.Function(func(ctx context.Context, _ queries.NoArguments) (profile, error) {
		principal, ok := identity.PrincipalFrom(ctx)
		if !ok {
			return profile{}, errors.New("no principal")
		}
		return profile{ID: principal.ID(), Name: principal.Name()}, nil
	}), queries.WithPath[queries.NoArguments](mePath),
		queries.WithAuthorization[queries.NoArguments](metadata.Authorization{
			Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"reader"}}},
		}))
}

func newServer(t *testing.T) *fixture.Application {
	t.Helper()
	// recipe:start jwt-compose
	bearer := jwtauth.Bearer(
		func(*jwt.Token) (any, error) { return signingKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
	)
	options := arc.Options{Authentication: []authentication.Handler{bearer}}
	// recipe:end
	return fixture.New(t, options, registerMe)
}

func claims(mutate func(*jwtauth.Claims)) *jwtauth.Claims {
	now := time.Now()
	c := &jwtauth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "ada",
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
		Name:     "Ada Lovelace",
		Roles:    []string{"reader"},
		TokenUse: "access",
	}
	if mutate != nil {
		mutate(c)
	}
	return c
}

func sign(t *testing.T, method jwt.SigningMethod, key any, c *jwtauth.Claims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, c).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func TestVerifiedBearerTokenBecomesTheArcPrincipal(t *testing.T) {
	server := fixture.Serve(t, newServer(t).App)
	r := fixture.Do(t, server, http.MethodGet, mePath, "", bearer(sign(t, jwt.SigningMethodHS256, signingKey, claims(nil))))
	if r.Status != http.StatusOK || !strings.Contains(r.Body, `"id":"ada"`) || !strings.Contains(r.Body, `"name":"Ada Lovelace"`) {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
}

func TestSchemeIsCaseInsensitive(t *testing.T) {
	server := fixture.Serve(t, newServer(t).App)
	header := http.Header{"Authorization": {"bearer " + sign(t, jwt.SigningMethodHS256, signingKey, claims(nil))}}
	if r := fixture.Do(t, server, http.MethodGet, mePath, "", header); r.Status != http.StatusOK {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
}

func TestRejectedTokensAreTerminal401s(t *testing.T) {
	server := fixture.Serve(t, newServer(t).App)
	past := jwt.NewNumericDate(time.Now().Add(-time.Hour))
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims(nil)).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"expired":         sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.ExpiresAt = past })),
		"no expiry":       sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.ExpiresAt = nil })),
		"wrong issuer":    sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.Issuer = "https://other.example" })),
		"wrong audience":  sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.Audience = jwt.ClaimStrings{"other"} })),
		"no subject":      sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.Subject = "" })),
		"ID token":        sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.TokenUse = "id" })),
		"no token kind":   sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.TokenUse = "" })),
		"not yet valid":   sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour)) })),
		"wrong key":       sign(t, jwt.SigningMethodHS256, []byte("another-test-only-hmac-key-0123456789abcd"), claims(nil)),
		"disallowed alg":  sign(t, jwt.SigningMethodHS384, signingKey, claims(nil)),
		"unsigned (none)": none,
		"malformed":       "not-a-jwt",
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{mePath, fixture.QueryPath} {
				r := fixture.Do(t, server, http.MethodGet, path, "", bearer(token))
				if r.Status != http.StatusUnauthorized || strings.Contains(r.Body, token) {
					t.Fatalf("%s: got %d %q", path, r.Status, r.Body)
				}
			}
		})
	}
}

func TestOtherSchemesStayAnonymous(t *testing.T) {
	server := fixture.Serve(t, newServer(t).App)
	basic := http.Header{"Authorization": {"Basic YWRhOnNlY3JldA=="}}
	if r := fixture.Do(t, server, http.MethodGet, fixture.QueryPath+"?title=public", "", basic); r.Status != http.StatusOK {
		t.Fatalf("public query: got %d %q", r.Status, r.Body)
	}
	// Arc, like C# Arc, answers an anonymous role denial with 403, not 401.
	for _, header := range []http.Header{nil, basic} {
		if r := fixture.Do(t, server, http.MethodGet, mePath, "", header); r.Status != http.StatusForbidden || !strings.Contains(r.Body, `"isAuthorized":false`) {
			t.Fatalf("protected query with %v: got %d %q", header, r.Status, r.Body)
		}
	}
}

func TestMalformedOrRepeatedAuthorizationIsTerminal(t *testing.T) {
	server := fixture.Serve(t, newServer(t).App)
	token := sign(t, jwt.SigningMethodHS256, signingKey, claims(nil))
	for name, values := range map[string][]string{
		"missing token": {"Bearer"},
		"blank token":   {"Bearer "},
		"extra field":   {"Bearer " + token + " extra"},
		"two headers":   {"Bearer " + token, "Bearer " + token},
		"mixed schemes": {"Basic YWRhOnNlY3JldA==", "Bearer " + token},
	} {
		t.Run(name, func(t *testing.T) {
			r := fixture.Do(t, server, http.MethodGet, fixture.QueryPath, "", http.Header{"Authorization": values})
			if r.Status != http.StatusUnauthorized || strings.Contains(r.Body, token) {
				t.Fatalf("got %d %q", r.Status, r.Body)
			}
		})
	}
}

func TestCancelledAuthenticationDoesNotResolveKeys(t *testing.T) {
	called := false
	handler := jwtauth.Bearer(func(*jwt.Token) (any, error) {
		called = true
		return signingKey, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header = bearer(sign(t, jwt.SigningMethodHS256, signingKey, claims(nil)))
	if _, err := handler.Authenticate(ctx, request); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error = %v, key resolver called = %v", err, called)
	}
}

func TestKeyResolutionFailureDeniesPublicQueries(t *testing.T) {
	handler := jwtauth.Bearer(func(*jwt.Token) (any, error) {
		return nil, errors.New("signing key unavailable")
	}, jwt.WithValidMethods([]string{"HS256"}))
	f := fixture.New(t, arc.Options{Authentication: []authentication.Handler{handler}})
	r := fixture.Do(t, fixture.Serve(t, f.App), http.MethodGet, fixture.QueryPath, "",
		bearer(sign(t, jwt.SigningMethodHS256, signingKey, claims(nil))))
	if r.Status != http.StatusUnauthorized || strings.Contains(r.Body, "signing key unavailable") {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
}

func TestConcurrentBearerRequestsKeepTheirPrincipals(t *testing.T) {
	server := fixture.Serve(t, newServer(t).App)
	var group sync.WaitGroup
	for _, subject := range []string{"ada", "grace", "alan", "margaret"} {
		token := sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.Subject = subject }))
		group.Go(func() {
			r := fixture.Do(t, server, http.MethodGet, mePath, "", bearer(token))
			if r.Status != http.StatusOK || !strings.Contains(r.Body, `"id":"`+subject+`"`) {
				t.Errorf("%s: got %d %q", subject, r.Status, r.Body)
			}
		})
	}
	group.Wait()
}

func TestMissingRoleIsForbidden(t *testing.T) {
	server := fixture.Serve(t, newServer(t).App)
	token := sign(t, jwt.SigningMethodHS256, signingKey, claims(func(c *jwtauth.Claims) { c.Roles = nil }))
	if r := fixture.Do(t, server, http.MethodGet, mePath, "", bearer(token)); r.Status != http.StatusForbidden {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
}
