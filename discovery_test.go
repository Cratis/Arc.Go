package arc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

func boolPointer(v bool) *bool { return &v }
func TestDiscoveryExposureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		o          arc.Options
		status     int
		buildError bool
	}{
		{"production unavailable", arc.Options{}, 404, false}, {"development", arc.Options{Environment: "Development"}, 200, false},
		{"production authenticated", arc.Options{Authentication: []authentication.Handler{authentication.HostPrincipal()}}, 401, false},
		{"explicit requires capability", arc.Options{Introspection: arc.IntrospectionOptions{RequireAuthentication: boolPointer(true)}}, 0, true},
		{"explicit anonymous", arc.Options{Introspection: arc.IntrospectionOptions{RequireAuthentication: boolPointer(false)}}, 200, false},
		{"roles cannot be anonymous", arc.Options{Introspection: arc.IntrospectionOptions{RequireAuthentication: boolPointer(false), Roles: []string{"admin"}}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := arc.NewBuilder(tc.o)
			if err != nil {
				t.Fatal(err)
			}
			a, err := buildStarted(t, b)
			if tc.buildError {
				if err == nil {
					t.Fatal("accepted invalid exposure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/.cratis/commands", "/.cratis/queries", "/.cratis/users", "/.cratis/tenants", "/.cratis/identity-details/schema"} {
				w := httptest.NewRecorder()
				a.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != tc.status {
					t.Fatal(path, w.Code, w.Body.String())
				}
				if w.Code == 200 && strings.Contains(w.Body.String(), "isSuccess") {
					t.Fatal("wrapped discovery")
				}
			}
		})
	}
}
func TestCatalogsSchemasAndRoles(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Namespace: "Company.Tasks", Routes: &metadata.Options{RoutePrefix: "api", SegmentsToSkip: 1, IncludeCommandName: true, IncludeQueryName: true, EnableQueryHTTPMethod: true}, Authentication: []authentication.Handler{authentication.HostPrincipal()}, Introspection: arc.IntrospectionOptions{Roles: []string{"reader"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[httpCommand](b); err != nil { // Needs an explicit handler.
		if !errors.Is(err, commands.ErrMissingHandler) {
			t.Fatal(err)
		}
		if err := commands.Register[httpCommand](b, commands.Invoke(func(context.Context, *commands.Invocation, httpCommand) (int, error) { return 0, nil })); err != nil {
			t.Fatal(err)
		}
	}
	if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, httpArguments) ([]builderModel, error) { return nil, nil })); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, roles := range [][]string{nil, {"reader"}} {
		r := httptest.NewRequest("GET", "/.cratis/commands", nil).WithContext(identity.WithPrincipal(t.Context(), identity.System(roles...)))
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if len(roles) == 0 {
			if w.Code != 403 {
				t.Fatal(w.Code)
			}
			continue
		}
		var entries []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0]["namespace"] != "Tasks" || entries[0]["type"] != "Company.Tasks.httpCommand" || entries[0]["route"] != "/api/tasks/http-command" || entries[0]["payloadSchema"] == nil || entries[0]["documentationSummary"] != "" {
			t.Fatal(w.Body.String())
		}
	}
}

type opaqueDetails struct{}

func (opaqueDetails) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }
func TestOpaqueSchemaRequiresOverrideWithoutActivation(t *testing.T) {
	for _, override := range []bool{false, true} {
		b, err := arc.NewBuilder(arc.Options{Environment: "Development"})
		if err != nil {
			t.Fatal(err)
		}
		if err := arc.RegisterIdentityDetails(b, "opaque", identity.DetailsProviderFunc[opaqueDetails](func(context.Context, identity.Context) (identity.Details[opaqueDetails], error) {
			t.Fatal("schema activated provider")
			return identity.Details[opaqueDetails]{}, nil
		})); err != nil {
			t.Fatal(err)
		}
		if override {
			if err := arc.RegisterSchema[opaqueDetails](b, json.RawMessage(`{"type":"string"}`)); err != nil {
				t.Fatal(err)
			}
		}
		_, err = b.Build()
		if override && err != nil || !override && !errors.Is(err, arc.ErrSchemaUnavailable) {
			t.Fatal(override, err)
		}
	}
}
