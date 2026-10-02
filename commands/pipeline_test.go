// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

type resource struct {
	closed   int
	closeErr error
}

func (r *resource) Close(context.Context) error { r.closed++; return r.closeErr }
func TestAuthorizationSuppressesOrdinaryFactories(t *testing.T) {
	var r commands.Registry
	ordinary, handled, validated, provided := 0, 0, 0, 0
	must(t, commands.Register[Clear](&r, commands.Scoped(func(context.Context, *resource) (commands.Handler[Clear, int], error) {
		handled++
		return commands.WithProvide(func(Clear, context.Context) (int, error) { provided++; return 1, nil }, func(Clear, context.Context, int) (int, error) { handled++; return 1, nil }), nil
	}),
		commands.WithAuthorization[Clear](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Editor"}}}}),
		commands.WithScopedValidator[Clear](func(context.Context, *execution.Scope) (validation.Validator[Clear], error) {
			validated++
			return validation.ValidatorFunc[Clear](func(context.Context, Clear) ([]validation.Result, error) { return nil, nil }), nil
		})))
	must(t, r.AddFilter("ordinary", func(context.Context, *execution.Scope) (commands.Filter, error) {
		ordinary++
		return commands.FilterFunc(func(_ context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
			return commands.Success(inv.CommandContext().CorrelationID()), nil
		}), nil
	}))
	resources := &resource{}
	p := build(t, &r, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return resources, nil }})
	result, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	if result.IsAuthorized() || ordinary != 0 || handled != 0 || validated != 0 || provided != 0 || resources.closed != 1 {
		t.Fatal(result.Details(), ordinary, handled, validated, provided, resources.closed)
	}
	ctx := identity.WithPrincipal(t.Context(), identity.System("Editor"))
	result, err = p.Execute(ctx, Clear{})
	must(t, err)
	if !result.IsSuccess() || ordinary != 1 || handled != 2 || validated != 1 || provided != 1 {
		t.Fatal(result.Details(), ordinary, handled, validated, provided)
	}
}
func TestMembershipAppliesToPublicCommands(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r))
	p := build(t, &r, commands.PipelineOptions{Membership: tenancy.MembershipFunc(func(context.Context, identity.Principal, tenancy.ID) (bool, error) { return false, nil })})
	result, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	if result.IsAuthorized() {
		t.Fatal("public command bypassed membership")
	}
}
func TestRequiredTenantBeforeResources(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r))
	p := build(t, &r, commands.PipelineOptions{RequireTenant: true, OpenResources: func(context.Context) (execution.Resources, error) { t.Fatal("opened without tenant"); return nil, nil }})
	result, err := p.Execute(t.Context(), Clear{})
	if !errors.Is(err, tenancy.ErrNotSet) || result.IsAuthorized() {
		t.Fatal(result.Details(), err)
	}
}
func TestScopedAndOwnedExecutionAndLifetime(t *testing.T) {
	var r commands.Registry
	var retained *commands.Invocation
	must(t, commands.Register[Clear](&r, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ Clear) (int, error) {
		retained = inv
		resources, err := execution.ResourcesAs[*resource](ctx, inv.Scope())
		if err != nil {
			return 0, err
		}
		if resources.closed != 0 {
			t.Fatal("closed during handling")
		}
		return 0, nil
	})))
	opened := &resource{}
	p := build(t, &r, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return opened, nil }})
	result, err := p.Execute(t.Context(), Clear{})
	must(t, err)
	if _, ok := result.Response(); !ok || opened.closed != 1 {
		t.Fatal(result.Details(), opened.closed)
	}
	if err := retained.SetValue("late", true); !errors.Is(err, commands.ErrExecutionClosed) {
		t.Fatal(err)
	}
	borrowed := &resource{}
	scope, err := execution.BorrowScope(t.Context(), borrowed)
	must(t, err)
	result, err = p.ExecuteScoped(t.Context(), scope, Clear{})
	must(t, err)
	if !result.IsSuccess() || borrowed.closed != 0 {
		t.Fatal(result.Details(), borrowed.closed)
	}
	must(t, scope.Close(t.Context()))
	if borrowed.closed != 0 {
		t.Fatal("borrowed holder closed")
	}
}
func TestPipelineOptionsAndTypedContractFailBeforeCallbacks(t *testing.T) {
	var r commands.Registry
	calls := 0
	must(t, commands.Register[Clear](&r, commands.Handle(func(Clear, context.Context) (string, error) { calls++; return "ok", nil })))
	p := build(t, &r, commands.PipelineOptions{})
	if _, err := p.Execute(t.Context(), Clear{}, commands.ExecuteOptions{}, commands.ExecuteOptions{}); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if _, err := commands.Execute[int](t.Context(), p, Clear{}); !errors.Is(err, commands.ErrResponseType) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("invalid call invoked handler")
	}
}
func TestResponseSuppressionAndRedactionAfterCleanupFailure(t *testing.T) {
	cause := errors.New("secret database detail")
	var r commands.Registry
	must(t, commands.Register(&r, commands.Handle(Rename.Handle)))
	p := build(t, &r, commands.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return &resource{closeErr: cause}, nil }})
	result, err := p.Execute(t.Context(), Rename{"response"})
	if !errors.Is(err, cause) || !result.HasExceptions() {
		t.Fatal(result.Details(), err)
	}
	if _, ok := result.Response(); ok {
		t.Fatal("failed cleanup retained response")
	}
	if messages := result.Details().ExceptionMessages; len(messages) != 1 || messages[0] != "An internal error occurred while processing the request. See server logs for details." {
		t.Fatal(messages)
	}
}
func TestFactoryBuildDoesNotActivateAndCatalogMismatchFails(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.WithHandlingDependencies[Clear](di.KeyFor[string]())))
	if _, err := r.Build(commands.PipelineOptions{}); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	must(t, commands.Register(&r, commands.Handle(Rename.Handle)))
	var auth authorization.Registry
	evaluator, err := auth.Build(metadata.Catalog{Version: metadata.Version}, authorization.Options{})
	must(t, err)
	if _, err := r.Build(commands.PipelineOptions{DependencyCatalog: allCatalog{}, Authorization: evaluator}); !errors.Is(err, authorization.ErrCatalogMismatch) {
		t.Fatal(err)
	}
	_ = build(t, &r, commands.PipelineOptions{DependencyCatalog: allCatalog{}})
}

type allCatalog struct{}

func (allCatalog) Contains(di.Key) bool { return true }
func TestApplicationPanicAndCancellationRemainInspectable(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Void(func(Clear, context.Context) error { panic("secret") })))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Execute(t.Context(), Clear{})
	var panicErr *execution.PanicError
	if !errors.As(err, &panicErr) || !result.HasExceptions() {
		t.Fatal(result.Details(), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err = p.Execute(ctx, Clear{})
	if !errors.Is(err, context.Canceled) || result.IsSuccess() {
		t.Fatal(result.Details(), err)
	}
}
