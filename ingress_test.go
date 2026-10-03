package arc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/identity"
)

func TestTerminalCredentialsAndCorrelation(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Authentication: []authentication.Handler{authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
		return authentication.Failed("SECRET"), nil
	}), authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
		t.Fatal("nonterminal credentials")
		return authentication.Anonymous(), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[builderCommand](b); err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/builder-command", strings.NewReader("{}"))
	r.Header["x-correlation-id"] = []string{"12345678-1234-1234-1234-123456789abc"}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), w.Header().Get("X-Correlation-ID")) || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	// Trusted ambient principals are ignored without an explicitly registered adapter.
	r = httptest.NewRequest("GET", "/.cratis/me", nil).WithContext(identity.WithPrincipal(t.Context(), identity.System()))
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
