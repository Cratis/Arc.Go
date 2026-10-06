package arc_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
)

func TestIngressErrorsAreLoggedWithoutRequestSecrets(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		authError, tenantError error
		status                 int
		level                  string
	}{
		{"authentication", errors.New("verifier unavailable"), nil, 500, "ERROR"},
		{"tenant server", nil, errors.New("selector unavailable"), 500, "ERROR"},
		{"tenant client", nil, tenancy.ErrAmbiguousSelection, 400, "WARN"},
		{"rejected credentials", nil, nil, 401, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, endpoint := range []struct{ method, path string }{{"POST", "/command"}, {"GET", "/query"}, {"GET", "/.cratis/me"}, {"GET", "/raw"}} {
				var logs bytes.Buffer
				b, err := arc.NewBuilder(arc.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Authentication: []authentication.Handler{authentication.HandlerFunc(func(context.Context, *http.Request) (authentication.Result, error) {
					if tc.authError != nil {
						return authentication.Result{}, tc.authError
					}
					if tc.status == 401 {
						return authentication.Failed("SECRET reason"), nil
					}
					return authentication.Anonymous(), nil
				})}, TenantResolver: tenancy.ResolverFunc(func(context.Context, *http.Request) (tenancy.ID, error) { return tenancy.Default(), tc.tenantError })})
				if err != nil {
					t.Fatal(err)
				}
				if err := commands.Register[builderCommand](b, commands.WithPath[builderCommand]("/command")); err != nil {
					t.Fatal(err)
				}
				if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]builderModel, error) { return nil, nil }), queries.WithPath[queries.NoArguments]("/query")); err != nil {
					t.Fatal(err)
				}
				if err := b.Handle("/raw", http.NotFoundHandler()); err != nil {
					t.Fatal(err)
				}
				a, err := buildStarted(t, b)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader("{}"))
				r.Header.Set("Authorization", "SECRET token")
				r.Header.Set("X-Custom", "SECRET header")
				w := httptest.NewRecorder()
				a.ServeHTTP(w, r)
				if w.Code != tc.status || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(logs.String(), "SECRET") {
					t.Fatal(endpoint, w.Code, w.Body.String(), logs.String())
				}
				if tc.level == "" {
					if logs.Len() != 0 {
						t.Fatal(logs.String())
					}
				} else if !strings.Contains(logs.String(), `"level":"`+tc.level+`"`) || !strings.Contains(logs.String(), `"error":`) {
					t.Fatal(logs.String())
				}
				if tc.status == 400 && endpoint.path == "/query" && (!strings.Contains(w.Body.String(), "malformedRequest") || strings.Contains(w.Body.String(), `"exceptionMessages":["An internal`)) {
					t.Fatal(w.Body.String())
				}
			}
		})
	}
}
