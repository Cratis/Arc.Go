package arc_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/tenancy"
)

func TestMiddlewareCompositionAndRawHandlerCapabilities(t *testing.T) {
	var logs bytes.Buffer
	order := []string{}
	constructors := 0
	b, err := arc.NewBuilder(arc.Options{Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if err := b.Use(func(next http.Handler) http.Handler {
			constructors++
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+" before")
				if correlation.FromContext(r.Context()).IsZero() {
					t.Fatal("missing ingress metadata")
				}
				next.ServeHTTP(w, r)
				order = append(order, name+" after")
			})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Handle("GET /health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, _ := tenancy.TenantFrom(r.Context())
		if tenant.String() != "team" {
			t.Fatal("raw tenant", tenant)
		}
		if _, ok := w.(http.Flusher); !ok {
			t.Fatal("lost flusher")
		}
		if _, ok := w.(http.Hijacker); ok {
			t.Fatal("invented hijacker")
		}
		w.WriteHeader(204)
	})); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/health?SECRET=credentials", nil)
	r.Header.Set("x-cratis-tenant-id", "team")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if constructors != 2 || w.Code != 204 || !slices.Equal(order, []string{"one before", "two before", "two after", "one after"}) {
		t.Fatal(constructors, w.Code, order)
	}
	if strings.Contains(logs.String(), "SECRET") || !strings.Contains(logs.String(), "status=204") {
		t.Fatal(logs.String())
	}
}
func TestMiddlewareConstructorFailuresAreConfigurationErrors(t *testing.T) {
	for _, m := range []arc.Middleware{func(http.Handler) http.Handler { return nil }, func(http.Handler) http.Handler { panic("constructor") }} {
		b, err := arc.NewBuilder(arc.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Use(m); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Build(); err == nil {
			t.Fatal("accepted broken constructor")
		}
	}
}
