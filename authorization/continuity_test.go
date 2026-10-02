package authorization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
)

// switchingContext models a host that incorrectly replaces its security values
// during an application callback. Only this synchronous test mutates it.
type switchingContext struct {
	context.Context
	values context.Context
}

func (c *switchingContext) Value(key any) any { return c.values.Value(key) }

func TestRecheckAfterPolicyCallback(t *testing.T) {
	ctx := &switchingContext{Context: t.Context(), values: identity.WithPrincipal(t.Context(), identity.System("Reader"))}
	var registry authorization.Registry
	register(t, &registry, "changes", false, func(context.Context, authorization.Context) (authorization.Decision, error) {
		ctx.values = identity.WithPrincipal(t.Context(), identity.System("Admin"))
		return authorization.Allow(), nil
	})
	register(t, &registry, "never", false, func(context.Context, authorization.Context) (authorization.Decision, error) {
		t.Fatal("callback ran after identity changed")
		return authorization.Allow(), nil
	})
	e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "changes"}, {Policy: "never"}}}), authorization.Options{})
	prepared, err := e.Prepare(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if decision, err := prepared.Evaluate(ctx, nil); decision.IsAllowed() || !errors.Is(err, authorization.ErrIdentityChanged) {
		t.Fatalf("continuity=%v %v", decision, err)
	}
}

func TestPolicyPanicRemainsBoundaryOwned(t *testing.T) {
	var registry authorization.Registry
	register(t, &registry, "panic", true, func(context.Context, authorization.Context) (authorization.Decision, error) {
		panic("local diagnostic")
	})
	e := build(t, &registry, catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "panic"}}}), authorization.Options{})
	prepared, err := e.Prepare(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if got := recover(); got != "local diagnostic" {
			t.Fatalf("panic=%v", got)
		}
	}()
	_, _ = prepared.Evaluate(t.Context(), nil)
}
