package arc_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/queries"
)

type httpArguments struct {
	Name string `json:"name"`
}

func TestGETHEADQUERYReadersAndSnapshotNegotiation(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[builderModel](b, "All", queries.Function(func(_ context.Context, a httpArguments) ([]builderModel, error) {
		return []builderModel{{Name: a.Name}}, nil
	}), queries.WithPath[httpArguments]("/items")); err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/items?name=one", "", 200, `"name":"one"`}, {"QUERY", "/items", `{"arguments":{"name":"two","unknown":1}}`, 200, `"name":"two"`}, {"QUERY", "/items", `{`, 400, "An internal error occurred while processing the request. See server logs for details."}, {"GET", "/items?name=%zz", "", 400, "isSuccess"}, {"HEAD", "/items?name=one", "", 200, ""},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Accept", "text/event-stream")
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.method == "QUERY" && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Header())
			}
			if tc.method == "HEAD" && (w.Body.Len() != 0 || w.Header().Get("Content-Length") == "") {
				t.Fatal(w.Body.String(), w.Header())
			}
			if strings.Contains(w.Header().Get("Content-Type"), "event-stream") {
				t.Fatal("snapshot became stream")
			}
		})
	}
}
