package arc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/queries"
)

func TestExactRoutingMethodsAndCanonicalPaths(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Environment: "Development"})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]builderModel, error) { return nil, nil }), queries.WithPath[queries.NoArguments]("/items/")); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{"DELETE", "/items/", 405, "GET, HEAD, QUERY"}, {"OPTIONS", "/.cratis/me", 405, "GET, HEAD"}, {"GET", "/items/child", 404, ""}, {"GET", "/items", 404, ""}, {"GET", "/ITEMS/", 404, ""}, {"GET", "/items//", 400, ""}, {"POST", "/a/../items/", 400, ""}, {"GET", "/%69tems/", 400, ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || w.Body.Len() != 0 || w.Header().Get("Allow") != tc.allow {
				t.Fatalf("%d %s %v", w.Code, w.Body.String(), w.Header())
			}
		})
	}
}
func TestRawCatchAllCannotStealFrameworkRoutes(t *testing.T) {
	for _, pattern := range []string{"/", "/items/", "/{rest...}", "GET example.invalid/", "GET /items/{rest...}"} {
		t.Run(pattern, func(t *testing.T) {
			b, err := arc.NewBuilder(arc.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]builderModel, error) {
				return []builderModel{{Name: "Arc"}}, nil
			}), queries.WithPath[queries.NoArguments]("/items/")); err != nil {
				t.Fatal(err)
			}
			calls := 0
			if err := b.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(204) })); err != nil {
				t.Fatal(err)
			}
			a, err := buildStarted(t, b)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				method, path string
				status       int
			}{{"GET", "/items/", 200}, {"POST", "/items/", 405}, {"GET", "/.cratis/me", 401}, {"GET", "/.cratis/commands", 404}, {"GET", "/.cratis/unmapped", 404}, {"GET", "/.CRATIS/unknown", 404}, {"GET", "/items/child", 204}} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(tc.method, tc.path, nil)
				r.Host = "example.invalid"
				a.ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatal(tc, w.Code, w.Body.String())
				}
			}
			if calls != 1 {
				t.Fatal("fallback stole Arc routes", calls)
			}
		})
	}
}

func TestRawExactOwnershipConflictsStillFailBuild(t *testing.T) {
	for _, pattern := range []string{"/items", "POST /items", "example.invalid/items", "/.cratis/me", "DELETE /.cratis/unmapped", "/items/{$}"} {
		b, err := arc.NewBuilder(arc.Options{})
		if err != nil {
			t.Fatal(err)
		}
		path := "/items"
		if pattern == "/items/{$}" {
			path = "/items/"
		}
		if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]builderModel, error) { return nil, nil }), queries.WithPath[queries.NoArguments](path)); err != nil {
			t.Fatal(err)
		}
		if err := b.Handle(pattern, http.NotFoundHandler()); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Build(); !errors.Is(err, arc.ErrRouteConflict) {
			t.Fatal(pattern, err)
		}
	}
}

func TestReservedUnknownRoutesBypassRawAuthentication(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Authentication: []authentication.Handler{authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
		t.Fatal("unmapped reserved route challenged")
		return authentication.Failed("rejected"), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Handle("/", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/.cratis/unknown", nil))
	if w.Code != 404 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
