package arc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

func TestAnonymousAuthorizationDenialIs403AndCredentialFailureIs401(t *testing.T) {
	for _, failed := range []bool{false, true} {
		b, err := arc.NewBuilder(arc.Options{Authentication: []authentication.Handler{authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
			if failed {
				return authentication.Failed("SECRET"), nil
			}
			return authentication.Anonymous(), nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		protected := metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"admin"}}}}
		if err := commands.Register[builderCommand](b, commands.WithPath[builderCommand]("/protected"), commands.WithAuthorization[builderCommand](protected)); err != nil {
			t.Fatal(err)
		}
		if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]builderModel, error) {
			t.Fatal("authorization bypass")
			return nil, nil
		}), queries.WithPath[queries.NoArguments]("/protected"), queries.WithAuthorization[queries.NoArguments](protected)); err != nil {
			t.Fatal(err)
		}
		a, err := buildStarted(t, b)
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"POST", "GET", "QUERY"} {
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest(method, "/protected", strings.NewReader("{}")))
			status := 403
			if failed {
				status = 401
			}
			if w.Code != status || !strings.Contains(w.Body.String(), `"isAuthorized":false`) {
				t.Fatal(method, w.Code, w.Body.String())
			}
			if method == "QUERY" && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Header())
			}
		}
	}
}

type failingResources struct{ closed *int }

func (r failingResources) Close(context.Context) error {
	*r.closed++
	return errors.New("cleanup failed")
}
func TestScopedIdentitySuppressesActivationAndCleanupSuppressesSuccess(t *testing.T) {
	opened, provided, closed := 0, 0, 0
	b, err := arc.NewBuilder(arc.Options{Authentication: []authentication.Handler{authentication.HostPrincipal()}, OpenResources: func(context.Context) (execution.Resources, error) { opened++; return failingResources{&closed}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := arc.RegisterScopedIdentityDetails(b, "scoped", func(context.Context, *execution.Scope) (identity.DetailsProvider[builderModel], error) {
		provided++
		return identity.DetailsProviderFunc[builderModel](func(context.Context, identity.Context) (identity.Details[builderModel], error) {
			return identity.Details[builderModel]{IsUserAuthorized: true, Value: builderModel{Name: "private"}}, nil
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/.cratis/me", nil)
		if authenticated {
			r = r.WithContext(identity.WithPrincipal(t.Context(), identity.System()))
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		want := 401
		if authenticated {
			want = 500
		}
		if w.Code != want || w.Body.Len() != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if opened != 1 || provided != 1 || closed != 1 {
		t.Fatal(opened, provided, closed)
	}
}
func TestDisabledCatalogsDoNotDisableIdentityDiscovery(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Environment: "Development", Introspection: arc.IntrospectionOptions{Enabled: boolPointer(false)}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/.cratis/commands", 404}, {"/.cratis/queries", 404}, {"/.cratis/users", 200}, {"/.cratis/identity-details/schema", 200}, {"/.cratis/me", 401}} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status {
			t.Fatal(tc, w.Code)
		}
	}
}
func FuzzCanonicalRouting(f *testing.F) {
	for _, path := range []string{"/api/items", "/api//items", "/api/../items", "/%61pi/items", "/日"} {
		f.Add(path)
	}
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		f.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		f.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() {
		if err := a.Shutdown(context.Background()); err != nil {
			f.Error(err)
		}
	})
	f.Fuzz(func(t *testing.T, path string) {
		if len(path) > 8192 {
			return
		}
		r, err := http.NewRequest("GET", "http://example.invalid"+path, nil)
		if err != nil {
			return
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code >= 300 && w.Code < 400 {
			t.Fatal("unexpected canonical redirect", path)
		}
	})
}
