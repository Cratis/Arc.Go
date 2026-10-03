package arc_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

type userList struct{ name string }

func (p userList) ProvideUsers(context.Context) ([]identity.User, error) {
	return []identity.User{{MicrosoftIdentity: identity.ClientPrincipal{UserID: p.name, UserRoles: []string{}, Claims: []identity.ClientPrincipalClaim{}}}}, nil
}

type tenantList struct{ tenant tenancy.Tenant }

func (p tenantList) ProvideTenants(context.Context) ([]tenancy.Tenant, error) {
	return []tenancy.Tenant{p.tenant}, nil
}
func TestDiscoveryProvidersAreLazyOrderedAndNotDeduplicated(t *testing.T) {
	calls := 0
	b, err := arc.NewBuilder(arc.Options{Environment: "Development"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := tenancy.ParseID("team")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if err := b.AddUsersProvider(name, func(context.Context, *execution.Scope) (arc.UsersProvider, error) {
			calls++
			return userList{name}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := b.AddTenantsProvider(name, func(context.Context, *execution.Scope) (arc.TenantsProvider, error) {
			calls++
			return tenantList{tenancy.Tenant{ID: id, Name: "duplicate"}}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("eager discovery factories")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/.cratis/users", nil))
	var users []identity.User
	if err := json.Unmarshal(w.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(users) != 2 || users[0].MicrosoftIdentity.UserID != "one" || users[1].MicrosoftIdentity.UserID != "two" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/.cratis/tenants", nil))
	var tenants []tenancy.Tenant
	if err := json.Unmarshal(w.Body.Bytes(), &tenants); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(tenants) != 2 || tenants[0] != tenants[1] || calls != 4 {
		t.Fatal(calls, w.Code, w.Body.String())
	}
}
