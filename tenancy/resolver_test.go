package tenancy_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

func resolver(t *testing.T, options tenancy.Options) tenancy.Resolver {
	t.Helper()
	r, err := tenancy.NewResolver(options)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSelectorDefaultsAndInputs(t *testing.T) {
	defaults := tenancy.DefaultOptions()
	if defaults.Header != "x-cratis-tenant-id" || defaults.QueryParameter != "tenantId" || defaults.ClaimType != "tenant_id" || defaults.FixedID.String() != "development" {
		t.Fatal(defaults)
	}
	request := httptest.NewRequest("GET", "/?tenantId=query", nil)
	request.Header.Set(defaults.Header, "header")
	ctx := identity.WithPrincipal(t.Context(), identity.NewPrincipal(identity.PrincipalData{AuthenticationType: "trusted", Claims: []identity.Claim{{Type: "tenant_id", Value: "claim"}, {Type: "tenant_id", Value: "ignored"}}}))
	for _, tc := range []struct {
		strategy tenancy.Strategy
		request  *http.Request
		want     string
	}{
		{tenancy.Header, request, "header"}, {tenancy.Query, request, "query"}, {tenancy.Claim, request, "claim"}, {tenancy.Claim, nil, "claim"}, {tenancy.Fixed, nil, "development"}, {tenancy.Header, nil, "[NotSet]"}, {tenancy.Query, nil, "[NotSet]"},
	} {
		got, err := resolver(t, tenancy.Options{Strategy: tc.strategy}).Resolve(ctx, tc.request)
		if err != nil || got.String() != tc.want {
			t.Fatalf("strategy=%d got=%v err=%v", tc.strategy, got, err)
		}
	}
	fixed, _ := tenancy.ParseID("configured")
	if got, err := resolver(t, tenancy.Options{Strategy: tenancy.Fixed, FixedID: fixed}).Resolve(t.Context(), nil); err != nil || got != fixed {
		t.Fatal(got, err)
	}
	absent := httptest.NewRequest("GET", "/", nil)
	if got, err := resolver(t, tenancy.Options{}).Resolve(t.Context(), absent); err != nil || got.IsSet() {
		t.Fatal(got, err)
	}
	request.Header.Add(defaults.Header, "other")
	if _, err := resolver(t, tenancy.Options{}).Resolve(t.Context(), request); !errors.Is(err, tenancy.ErrAmbiguousSelection) {
		t.Fatal(err)
	}
	request.URL.RawQuery = "tenantId=one&tenantId=two"
	if _, err := resolver(t, tenancy.Options{Strategy: tenancy.Query}).Resolve(t.Context(), request); !errors.Is(err, tenancy.ErrAmbiguousSelection) {
		t.Fatal(err)
	}
	request.URL.RawQuery = "tenantId=%zz"
	if _, err := resolver(t, tenancy.Options{Strategy: tenancy.Query}).Resolve(t.Context(), request); !errors.Is(err, tenancy.ErrInvalidID) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolver(t, tenancy.Options{Strategy: tenancy.Fixed}).Resolve(canceled, nil); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestSubdomainHostMatrix(t *testing.T) {
	options := tenancy.Options{Strategy: tenancy.Subdomain, BaseDomain: ".Example.COM.:443"}
	r := resolver(t, options)
	options.BaseDomain = "changed.test"
	options.Header = "changed"
	for _, tc := range []struct {
		host, want string
		err        error
	}{
		{"ACME.EXAMPLE.COM", "acme", nil}, {"acme.example.com.:8080", "acme", nil}, {".acme.example.com.", "acme", nil},
		{"example.com", "fallback", nil}, {"deep.acme.example.com", "fallback", nil}, {"acme.otherexample.com", "fallback", nil},
		{"acme.example.com.evil", "fallback", nil}, {"127.0.0.1:80", "fallback", nil}, {"[::1]:80", "fallback", nil}, {"::1", "fallback", nil},
		{"-bad.example.com", "fallback", nil}, {"bad-.example.com", "fallback", nil}, {"bad_name.example.com", "fallback", nil},
		{"acme..example.com", "fallback", nil}, {"acme.example.com:bogus", "fallback", nil}, {"acme.example.com:65536", "fallback", nil},
		{"xn--bcher-kva.example.com", "xn--bcher-kva", nil}, {"bücher.example.com", "", tenancy.ErrUnsupportedHost},
		{"acme。example.com", "", tenancy.ErrUnsupportedHost}, {"", "fallback", nil},
	} {
		request := httptest.NewRequest("GET", "/", nil)
		request.Host = tc.host
		request.Header.Set("x-cratis-tenant-id", "fallback")
		request.Header.Set("X-Forwarded-Host", "evil.example.com")
		got, err := r.Resolve(t.Context(), request)
		if !errors.Is(err, tc.err) || (err == nil && got.String() != tc.want) {
			t.Errorf("host %q = %v,%v want=%q,%v", tc.host, got, err, tc.want, tc.err)
		}
	}
	for _, base := range []string{"", "localhost", "127.0.0.1", "127.0.0.1.", "[::1]:80", "bad_.test", "a..test", "example.com:no"} {
		if _, err := tenancy.NewResolver(tenancy.Options{Strategy: tenancy.Subdomain, BaseDomain: base}); !errors.Is(err, tenancy.ErrInvalidOptions) {
			t.Errorf("base %q err=%v", base, err)
		}
	}
	if _, err := tenancy.NewResolver(tenancy.Options{Strategy: tenancy.Subdomain, BaseDomain: "bücher.example"}); !errors.Is(err, tenancy.ErrUnsupportedHost) || !errors.Is(err, tenancy.ErrInvalidOptions) {
		t.Fatal(err)
	}
}

func TestInvalidOptionsAndMembershipSeparation(t *testing.T) {
	for _, options := range []tenancy.Options{{Strategy: 99}, {Header: "bad header"}, {Header: "x:tenant"}, {QueryParameter: " "}, {ClaimType: "tenant\n"}} {
		if _, err := tenancy.NewResolver(options); !errors.Is(err, tenancy.ErrInvalidOptions) {
			t.Fatal(options, err)
		}
	}
	if !errors.Is(tenancy.Require(tenancy.ID{}), tenancy.ErrNotSet) || tenancy.Require(tenancy.Default()) != nil {
		t.Fatal("tenant requirement")
	}
	calls := 0
	membership := tenancy.MembershipFunc(func(context.Context, identity.Principal, tenancy.ID) (bool, error) { calls++; return false, nil })
	tenant, err := resolver(t, tenancy.Options{Strategy: tenancy.Fixed}).Resolve(t.Context(), nil)
	if err != nil || calls != 0 {
		t.Fatal("selection authorized membership", err)
	}
	allowed, err := membership.Authorize(t.Context(), identity.System(), tenant)
	if err != nil || allowed || calls != 1 {
		t.Fatal("membership ignored", err)
	}
}

func FuzzResolveHost(f *testing.F) {
	for _, host := range []string{"acme.example.com", "bücher.example.com", "[::1]:80", "bad..example.com", "acme.example.com:443"} {
		f.Add(host)
	}
	r, err := tenancy.NewResolver(tenancy.Options{Strategy: tenancy.Subdomain, BaseDomain: "example.com"})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, host string) {
		request := &http.Request{Host: host}
		tenant, err := r.Resolve(t.Context(), request)
		if err == nil && tenant.IsSet() {
			label := tenant.String()
			if len(label) > 63 || strings.Contains(label, ".") {
				t.Fatal("invalid selected label")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					t.Fatal("invalid selected label")
				}
			}
		}
	})
}

func ExampleMembershipFunc() {
	resolver, err := tenancy.NewResolver(tenancy.Options{Strategy: tenancy.Fixed})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	tenant, err := resolver.Resolve(ctx, nil)
	if err != nil {
		panic(err)
	}
	membership := tenancy.MembershipFunc(func(_ context.Context, principal identity.Principal, tenant tenancy.ID) (bool, error) {
		return principal.HasRole("developer") && tenant.String() == "development", nil
	})
	allowed, err := membership.Authorize(ctx, identity.System("developer"), tenant)
	if err != nil {
		panic(err)
	}
	fmt.Println(tenant, allowed)
	// Output: development true
}
