package arc_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/identity"
)

func TestIdentityFreshUnwrappedAndLegacyCookieExpired(t *testing.T) {
	calls := 0
	b, err := arc.NewBuilder(arc.Options{Authentication: []authentication.Handler{authentication.HostPrincipal()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := arc.RegisterIdentityDetails(b, "custom", identity.DetailsProviderFunc[builderModel](func(context.Context, identity.Context) (identity.Details[builderModel], error) {
		calls++
		return identity.Details[builderModel]{IsUserAuthorized: true, Value: builderModel{Name: "fresh"}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("Build activated provider")
	}
	for _, authenticated := range []bool{false, true, true} {
		r := httptest.NewRequest("GET", "/.cratis/me", nil)
		r.Header.Set("Cookie", ".cratis-identity=forged")
		if authenticated {
			r = r.WithContext(identity.WithPrincipal(t.Context(), identity.System("admin")))
		}
		w := httptest.NewRecorder()
		w.Header().Set("Vary", "Origin")
		a.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store, private" || w.Header().Get("Vary") != "Origin, Cookie" || !strings.Contains(w.Header().Get("Set-Cookie"), "Expires=") {
			t.Fatal(w.Header())
		}
		if authenticated {
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"fresh"`) || strings.Contains(w.Body.String(), "isSuccess") {
				t.Fatal(w.Code, w.Body.String())
			}
		} else if w.Code != 401 || w.Body.Len() != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestLegacyCookieExpiryMatchesCSharpRemovalAttributes(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"http", "https"} {
		for _, present := range []bool{false, true} {
			r := httptest.NewRequest("GET", scheme+"://example.invalid/.cratis/me", nil)
			if present {
				r.Header.Set("Cookie", ".cratis-identity=SECRET")
			}
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			response := w.Result()
			cookies := response.Cookies()
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if !present {
				if len(cookies) != 0 {
					t.Fatal(cookies)
				}
				continue
			}
			if len(cookies) != 1 {
				t.Fatal(cookies)
			}
			c := cookies[0]
			if c.Name != ".cratis-identity" || c.Value != "" || c.Path != "/" || c.Domain != "" || c.HttpOnly || c.Secure || c.SameSite != 0 || c.MaxAge != 0 || !c.Expires.Before(time.Now()) || c.Expires.Before(time.Now().Add(-25*time.Hour)) {
				t.Fatal(scheme, c)
			}
			for _, attribute := range []string{"HttpOnly", "Secure", "SameSite", "Max-Age", "SECRET"} {
				if strings.Contains(w.Header().Get("Set-Cookie"), attribute) {
					t.Fatal(w.Header())
				}
			}
		}
	}
}

func TestIdentityAmbiguityFailsWithoutActivation(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := identity.DetailsProviderFunc[struct{}](func(context.Context, identity.Context) (identity.Details[struct{}], error) {
		t.Fatal("activated")
		return identity.Details[struct{}]{}, nil
	})
	if err := arc.RegisterIdentityDetails(b, "one", p); err != nil {
		t.Fatal(err)
	}
	if err := arc.RegisterIdentityDetails(b, "two", p); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(); err == nil {
		t.Fatal("ambiguous provider accepted")
	}
}
