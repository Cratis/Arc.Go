package arc_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

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
	a, err := b.Build()
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
		if w.Header().Get("Cache-Control") != "no-store, private" || w.Header().Get("Vary") != "Origin, Cookie" || !strings.Contains(w.Header().Get("Set-Cookie"), "Max-Age=0") {
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
