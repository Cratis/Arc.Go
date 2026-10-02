package authorization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/metadata"
)

var create = authorization.Target{Kind: authorization.Command, Identity: "Create"}

func catalog(declaration *metadata.Authorization) metadata.Catalog {
	return metadata.Catalog{Version: 1, Commands: []metadata.Command{{Type: metadata.TypeName{Name: "Create"}, Authorization: declaration}}}
}
func register(t *testing.T, r *authorization.Registry, name string, guest bool, callback authorization.PolicyFunc) {
	t.Helper()
	if err := r.Register(name, callback, authorization.PolicyOptions{EvaluatesAnonymous: guest}); err != nil {
		t.Fatal(err)
	}
}
func build(t *testing.T, r *authorization.Registry, c metadata.Catalog, options authorization.Options) *authorization.Evaluator {
	t.Helper()
	evaluator, err := r.Build(c, options)
	if err != nil {
		t.Fatal(err)
	}
	return evaluator
}

func TestBuildValidationAndFreeze(t *testing.T) {
	allow := authorization.PolicyFunc(func(context.Context, authorization.Context) (authorization.Decision, error) {
		t.Fatal("Build invoked policy")
		return authorization.Allow(), nil
	})
	for _, tc := range []struct {
		name        string
		declaration *metadata.Authorization
		kind        error
	}{
		{"unknown", &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "missing"}}}, authorization.ErrUnknownPolicy},
		{"scheme", &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{AuthenticationSchemes: []string{"bearer"}}}}, authorization.ErrUnsupportedScheme},
		{"contradiction", &metadata.Authorization{AllowAnonymous: true, Requirements: []metadata.AuthorizationRequirement{{Policy: "known"}}}, authorization.ErrInvalidConfiguration},
		{"empty role", &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{" "}}}}, authorization.ErrInvalidConfiguration},
		{"policy whitespace", &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: " known"}}}, authorization.ErrInvalidConfiguration},
		{"scheme malformed", &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{AuthenticationSchemes: []string{""}}}}, authorization.ErrInvalidConfiguration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var registry authorization.Registry
			register(t, &registry, "known", false, allow)
			if _, err := registry.Build(catalog(tc.declaration), authorization.Options{}); !errors.Is(err, tc.kind) {
				t.Fatalf("error=%v want=%v", err, tc.kind)
			}
			register(t, &registry, "afterFailure", false, allow)
			build(t, &registry, catalog(nil), authorization.Options{})
			if err := registry.Register("next", allow, authorization.PolicyOptions{}); !errors.Is(err, authorization.ErrFrozen) {
				t.Fatal(err)
			}
			if _, err := registry.Build(catalog(nil), authorization.Options{}); !errors.Is(err, authorization.ErrFrozen) {
				t.Fatal(err)
			}
		})
	}
	var registry authorization.Registry
	var nilPolicy authorization.PolicyFunc
	for _, policy := range []authorization.Policy{nil, nilPolicy} {
		if err := registry.Register("nil", policy, authorization.PolicyOptions{}); !errors.Is(err, authorization.ErrInvalidConfiguration) {
			t.Fatal(err)
		}
	}
	register(t, &registry, "known", false, allow)
	if err := registry.Register("known", allow, authorization.PolicyOptions{}); !errors.Is(err, authorization.ErrDuplicate) {
		t.Fatal(err)
	}
	duplicate := catalog(nil)
	duplicate.Commands = append(duplicate.Commands, duplicate.Commands[0])
	if _, err := registry.Build(duplicate, authorization.Options{}); !errors.Is(err, authorization.ErrDuplicate) {
		t.Fatal(err)
	}
	bad := catalog(nil)
	bad.Version = 0
	if _, err := registry.Build(bad, authorization.Options{}); !errors.Is(err, authorization.ErrInvalidConfiguration) {
		t.Fatal(err)
	}
	for _, name := range []string{"bad name", "Bad.Name", ""} {
		bad = catalog(nil)
		bad.Commands[0].Type.Name = name
		if _, err := registry.Build(bad, authorization.Options{}); !errors.Is(err, authorization.ErrInvalidConfiguration) {
			t.Fatal(err)
		}
	}
	overridden := metadata.Catalog{Version: 1, Queries: []metadata.Query{{ReadModel: metadata.TypeName{Name: "Item"}, Name: "All", Authorization: &metadata.Authorization{AllowAnonymous: true}, ReadModelAuthorization: &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "missing"}}}}}}
	if _, err := registry.Build(overridden, authorization.Options{}); !errors.Is(err, authorization.ErrUnknownPolicy) {
		t.Fatal(err)
	}
}
