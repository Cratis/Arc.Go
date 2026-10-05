---
title: Authenticate JWT bearer tokens
description: Verify bearer tokens with golang-jwt v5 in an Arc authentication handler.
---

Your frontend or API clients send `Authorization: Bearer <token>` and your
commands and queries must know who is calling. Arc does not parse JWTs itself:
it runs the authentication handlers you register and authorizes against the
principal they return. This recipe supplies that handler with
[golang-jwt v5](https://github.com/golang-jwt/jwt).

## Write the handler

The handler maps a verified token onto an Arc principal. This code is compiled
and tested in the [recipes module](index.md):

```go
// Claims are the token claims this recipe maps onto an Arc principal.
type Claims struct {
    jwt.RegisteredClaims
    Name  string   `json:"name,omitempty"`
    Roles []string `json:"roles,omitempty"`
}

// Bearer returns an Arc authentication handler for "Authorization: Bearer"
// tokens. Requests without that scheme stay anonymous for later handlers and
// authorization; a bearer token that does not verify is a terminal 401.
// Supply jwt.WithValidMethods, issuer, audience and expiry options: the
// parser accepts whatever the options do not forbid.
func Bearer(key jwt.Keyfunc, options ...jwt.ParserOption) authentication.Handler {
    parser := jwt.NewParser(options...)
    return authentication.HandlerFunc(func(_ context.Context, r *http.Request) (authentication.Result, error) {
        scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
        if !found || !strings.EqualFold(scheme, "Bearer") {
            return authentication.Anonymous(), nil
        }
        claims := &Claims{}
        if _, err := parser.ParseWithClaims(strings.TrimSpace(token), claims, key); err != nil {
            // Reasons stay local; never echo token text or parser details.
            if errors.Is(err, jwt.ErrTokenExpired) {
                return authentication.Failed("expired bearer token"), nil
            }
            return authentication.Failed("invalid bearer token"), nil
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
```

`authentication.Failed` is terminal: Arc answers 401 and runs no command or
query, even a public one. `authentication.Anonymous` lets the next handler try,
then leaves authorization to decide.

## Register it with Arc

Pin the accepted algorithm, issuer and audience, and require an expiry:

```go
bearer := jwtauth.Bearer(
    func(*jwt.Token) (any, error) { return signingKey, nil },
    jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
    jwt.WithIssuer(issuer),
    jwt.WithAudience(audience),
    jwt.WithExpirationRequired(),
)
options := arc.Options{Authentication: []authentication.Handler{bearer}}
```

Pass `options` to `arc.NewBuilder`. Handlers then read the caller with
`identity.PrincipalFrom(ctx)`, and role requirements in
`metadata.Authorization` match the token's `roles` claim.

:::danger[Always pin the algorithm]
Without `jwt.WithValidMethods`, the key function decides which algorithms are
acceptable. Pin the exact algorithm your issuer uses, and for asymmetric keys
return only the public key matching the token's `kid`.
:::

## Responses your clients see

| Request | Status |
| --- | --- |
| Valid token with a required role | 200 |
| Valid token without a required role | 403 |
| Expired, unsigned (`none`), wrongly signed, disallowed algorithm, wrong issuer or audience, no expiry, no subject, malformed | 401 |
| No `Authorization` header, or another scheme such as `Basic`, on a role-protected operation | 403 |
| No `Authorization` header on a public operation | 200 |

The 403 for an anonymous caller on a role-protected operation matches C# Arc:
role denial is authorization, not authentication. The response body never
contains the token.

## When this is the wrong fit

Use your identity provider's middleware or an authenticating ingress if it
already validates tokens and fetches signing keys; then supply the result with
`identity.WithPrincipal` and `authentication.HostPrincipal()`. This recipe does
not fetch or rotate JWKS keys. See [authentication](../authentication/index.md).
