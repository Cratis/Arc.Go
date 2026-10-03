package authentication_test

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/cratis/arc.go/authentication"
)

func TestForwardedIdentityExplicitTrustAndReservedClaims(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("x-ms-client-principal-id", "verified")
	r.Header.Set("x-ms-client-principal-name", "required")
	r.Header.Set("x-ms-client-principal", base64.StdEncoding.EncodeToString([]byte(`{"identityProvider":"trusted","userDetails":"name","userRoles":["admin"],"claims":[{"typ":"sub","val":"forged"},{"typ":"URN:CRATIS:ARC:IDENTITY:PROVIDER","val":"forged"}]}`)))
	for _, trust := range []bool{false, true} {
		handler, err := authentication.MicrosoftIdentityPlatform(authentication.MicrosoftIdentityOptions{TrustForwardedIdentityHeaders: trust})
		if err != nil {
			t.Fatal(err)
		}
		result, err := handler.Authenticate(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		principal, ok := result.Principal()
		if !trust {
			if ok || result.Failure() != nil {
				t.Fatal("untrusted header used")
			}
			continue
		}
		if !ok || principal.ID() != "verified" || !principal.HasRole("admin") {
			t.Fatal(principal)
		}
		if sub, _ := principal.Claim("sub"); sub != "verified" {
			t.Fatal(sub)
		}
		if provider, _ := principal.Claim(authentication.MicrosoftIdentityProviderClaim); provider != "trusted" {
			t.Fatal(provider)
		}
	}
	handler, err := authentication.MicrosoftIdentityPlatform(authentication.MicrosoftIdentityOptions{TrustForwardedIdentityHeaders: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"invalid", "bnVsbA=="} {
		r.Header.Set("x-ms-client-principal", value)
		result, err := handler.Authenticate(t.Context(), r)
		if err != nil || result.Failure() == nil {
			t.Fatal(result, err)
		}
	}
	r.Header.Add("x-ms-client-principal-id", "duplicate")
	result, err := handler.Authenticate(t.Context(), r)
	if err != nil || result.Failure() == nil {
		t.Fatal(result, err)
	}
	r.Header.Del("x-ms-client-principal-id")
	result, err = handler.Authenticate(t.Context(), r)
	if err != nil || result.Failure() != nil {
		t.Fatal(result, err)
	}
}
