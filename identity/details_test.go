package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cratis/arc.go/identity"
)

func TestDetailsFreshSnapshotAndNonAuthority(t *testing.T) {
	principal := identity.NewPrincipal(identity.PrincipalData{AuthenticationType: "verified", Roles: []string{"Reader"}, Claims: []identity.Claim{{Type: "sub", Value: "claimSubject"}}})
	ctx := identity.WithPrincipal(t.Context(), principal)
	type malicious struct {
		Roles           []string
		IsAuthenticated bool
	}
	calls := 0
	provider := identity.DetailsProviderFunc[malicious](func(got context.Context, value identity.Context) (identity.Details[malicious], error) {
		calls++
		if got != ctx || value.ID() != "unknown" || value.Name() != "unknown" {
			t.Error("display context")
		}
		claims := value.Claims()
		claims[0].Value = "modified"
		if value.Claims()[0].Value != "claimSubject" {
			t.Error("claims aliased")
		}
		return identity.Details[malicious]{IsUserAuthorized: true, Value: malicious{Roles: []string{"Admin"}, IsAuthenticated: false}}, nil
	})
	first, err := identity.ProvideDetails(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	first.Roles[0] = "mutated"
	second, err := identity.ProvideDetails(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || second.Roles[0] != "Reader" || !second.IsAuthenticated {
		t.Fatal("view authority/caching")
	}
	current, _ := identity.PrincipalFrom(ctx)
	if !current.Equal(principal) || current.ID() != "" {
		t.Fatal("provider changed principal")
	}
	data, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"unknown","name":"unknown","isAuthenticated":true,"isAuthorized":true,"roles":["Reader"],"details":{"isAuthenticated":false,"roles":["Admin"]}}`
	if string(data) != want {
		t.Fatalf("wire=%s", data)
	}
}

func TestDetailsDenialErrorsAndCancellation(t *testing.T) {
	calls := 0
	provider := identity.DetailsProviderFunc[any](func(context.Context, identity.Context) (identity.Details[any], error) {
		calls++
		return identity.Details[any]{}, nil
	})
	if _, err := identity.ProvideDetails(t.Context(), provider); !errors.Is(err, identity.ErrUnauthenticated) || calls != 0 {
		t.Fatal("anonymous provider invoked", err)
	}
	ctx := identity.WithPrincipal(t.Context(), identity.System())
	if _, err := identity.ProvideDetails(ctx, provider); !errors.Is(err, identity.ErrDetailsDenied) || calls != 1 {
		t.Fatal(err)
	}
	var nilFunc identity.DetailsProviderFunc[any]
	if _, err := identity.ProvideDetails(ctx, nilFunc); !errors.Is(err, identity.ErrInvalidProvider) {
		t.Fatal(err)
	}
	sentinel := errors.New("application error")
	provider = func(context.Context, identity.Context) (identity.Details[any], error) {
		return identity.Details[any]{IsUserAuthorized: true}, sentinel
	}
	if _, err := identity.ProvideDetails(ctx, provider); err != sentinel {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	provider = func(context.Context, identity.Context) (identity.Details[any], error) {
		cancel()
		return identity.Details[any]{IsUserAuthorized: true}, nil
	}
	if _, err := identity.ProvideDetails(canceled, provider); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestViewNilDetailsAndBoundedRecursion(t *testing.T) {
	view := identity.View[any]{}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"id":"","name":"","isAuthenticated":false,"isAuthorized":false,"roles":[],"details":null}` {
		t.Fatal(string(data))
	}
	view.Details = &view
	if _, err := json.Marshal(view); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatal("cyclic details", err)
	}
	sentinel := errors.New("encoding failed")
	if _, err := view.MarshalJSONWith(func(any) ([]byte, error) { return nil, sentinel }); err != sentinel {
		t.Fatal(err)
	}
}

func ExampleProvideDetails() {
	provider := identity.DetailsProviderFunc[string](func(_ context.Context, value identity.Context) (identity.Details[string], error) {
		return identity.Details[string]{IsUserAuthorized: true, Value: "Hello, " + value.Name()}, nil
	})
	ctx := identity.WithPrincipal(context.Background(), identity.System("jobs"))
	view, err := identity.ProvideDetails(ctx, provider)
	if err != nil {
		panic(err)
	}
	fmt.Println(view.Details, view.Roles)
	// Output: Hello, [System] [jobs]
}
