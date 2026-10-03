package arc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	arc "github.com/cratis/arc.go"
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
	a, err := b.Build()
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
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Handle("/", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(); err == nil {
		t.Fatal("catch-all accepted")
	}
}
