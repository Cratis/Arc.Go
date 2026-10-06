// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

type plainResources struct {
	closes   int
	closeErr error
}

func (r *plainResources) Close(context.Context) error { r.closes++; return r.closeErr }
func continueFilter() queries.Result[any] {
	return queries.NewResult[any](queries.Details{Authorized: true}, serialization.Optional[any]{})
}
func TestAuthorizationSuppressesOrdinaryFactoriesAndDefaultNeverBypasses(t *testing.T) {
	var r queries.Registry
	events := []string{}
	mustRegister(t, queries.Register[Item](&r, "Protected", queries.Invoke(func(context.Context, *queries.Invocation, queries.NoArguments) (Item, error) {
		events = append(events, "performer")
		return Item{}, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{}), queries.WithScopedValidator[queries.NoArguments](func(context.Context, *execution.Scope) (validation.Validator[queries.NoArguments], error) {
		events = append(events, "validator")
		return validation.ValidatorFunc[queries.NoArguments](func(context.Context, queries.NoArguments) ([]validation.Result, error) { return nil, nil }), nil
	})))
	mustRegister(t, r.AddAuthorizationFilter("auth", func(context.Context, *execution.Scope) (queries.AuthorizationFilter, error) {
		events = append(events, "auth filter")
		return queries.AsAuthorizationFilter(queries.FilterFunc(func(context.Context, *queries.Invocation) (queries.Result[any], error) { return continueFilter(), nil })), nil
	}))
	mustRegister(t, r.AddFilter("ordinary", func(context.Context, *execution.Scope) (queries.Filter, error) {
		events = append(events, "ordinary filter")
		return queries.FilterFunc(func(context.Context, *queries.Invocation) (queries.Result[any], error) { return continueFilter(), nil }), nil
	}))
	holder := &plainResources{}
	p := build(t, &r, queries.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) {
		events = append(events, "open")
		return holder, nil
	}})
	result, err := p.Perform(context.Background(), "Item.Protected", queries.Request{})
	if err != nil || result.IsAuthorized() || !result.IsReady() || holder.closes != 1 || !reflect.DeepEqual(events, []string{"open"}) {
		t.Fatalf("result=%+v err=%v events=%v closes=%d", result.Details(), err, events, holder.closes)
	}
}
func TestAuthorizationFilterOrderAndAllFindingsBlock(t *testing.T) {
	for _, severity := range []validation.Severity{validation.Unknown, validation.Information, validation.Warning, validation.Error} {
		t.Run(severityName(severity), func(t *testing.T) {
			var r queries.Registry
			events := []string{}
			mustRegister(t, queries.Register[Item](&r, "Current", queries.Function(func(context.Context, queries.NoArguments) (Item, error) {
				events = append(events, "performer")
				return Item{}, nil
			}), public[queries.NoArguments](), queries.WithValidator(validation.ValidatorFunc[queries.NoArguments](func(context.Context, queries.NoArguments) ([]validation.Result, error) {
				events = append(events, "validate")
				return []validation.Result{{Severity: severity, Message: "blocked"}}, nil
			}))))
			mustRegister(t, r.AddFilter("ordinary", func(context.Context, *execution.Scope) (queries.Filter, error) {
				events = append(events, "ordinary factory")
				return queries.FilterFunc(func(context.Context, *queries.Invocation) (queries.Result[any], error) {
					events = append(events, "ordinary")
					return continueFilter(), nil
				}), nil
			}))
			mustRegister(t, r.AddAuthorizationFilter("auth", func(context.Context, *execution.Scope) (queries.AuthorizationFilter, error) {
				events = append(events, "auth factory")
				return queries.AsAuthorizationFilter(queries.FilterFunc(func(context.Context, *queries.Invocation) (queries.Result[any], error) {
					events = append(events, "auth")
					return continueFilter(), nil
				})), nil
			}))
			p := build(t, &r, queries.PipelineOptions{})
			result, err := p.Perform(context.Background(), "Item.Current", queries.Request{})
			if err != nil || result.IsValid() || !reflect.DeepEqual(events, []string{"auth factory", "auth", "ordinary factory", "ordinary", "validate"}) {
				t.Fatalf("events=%v result=%+v err=%v", events, result.Details(), err)
			}
		})
	}
}
func severityName(s validation.Severity) string {
	return []string{"unknown", "information", "warning", "error"}[s]
}

type modelValidatedArgs struct {
	Name string `json:"name" validate:"required"`
}

func (a modelValidatedArgs) Validate(context.Context) ([]validation.Result, error) {
	if a.Name == "deny" {
		return []validation.Result{{Severity: validation.Warning, Message: "model rejected", Members: []string{"name"}}}, nil
	}
	return nil, nil
}
func TestModelMethodsTagsGraphAndExplicitOptOut(t *testing.T) {
	var r queries.Registry
	calls := 0
	performer := queries.Function(func(context.Context, modelValidatedArgs) (Item, error) { calls++; return Item{}, nil })
	mustRegister(t, queries.Register[Item](&r, "Normal", performer, public[modelValidatedArgs]()))
	mustRegister(t, queries.Register[Item](&r, "OptOut", performer, public[modelValidatedArgs](), queries.WithoutModelValidation[modelValidatedArgs]()))
	p := build(t, &r, queries.PipelineOptions{})
	for _, name := range []string{"", "deny"} {
		result, err := p.Perform(context.Background(), "Item.Normal", queries.RequestFor(modelValidatedArgs{Name: name}, queries.Parameters{}))
		if err != nil || result.IsValid() || calls != 0 {
			t.Fatalf("result=%+v err=%v", result.Details(), err)
		}
	}
	result, err := p.Perform(context.Background(), "Item.OptOut", queries.RequestFor(modelValidatedArgs{}, queries.Parameters{}))
	if err != nil || !result.IsSuccess() || calls != 1 {
		t.Fatal("opt out did not apply", err)
	}
}
func TestMembershipRunsOnAnonymousQueryAndPolicyRecheck(t *testing.T) {
	var r queries.Registry
	calls := 0
	mustRegister(t, queries.Register[Item](&r, "Public", itemPerformer(), public[queries.NoArguments]()))
	p := build(t, &r, queries.PipelineOptions{Membership: tenancy.MembershipFunc(func(context.Context, identity.Principal, tenancy.ID) (bool, error) { calls++; return false, nil })})
	result, err := p.Perform(context.Background(), "Item.Public", queries.Request{})
	if err != nil || result.IsAuthorized() || calls != 1 {
		t.Fatal("public membership bypass", err)
	}
	var protected queries.Registry
	mustRegister(t, queries.Register[Item](&protected, "Protected", itemPerformer(), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "Recheck"}}})))
	var policies authorization.Registry
	policyCalls := 0
	mustRegister(t, policies.Register("Recheck", authorization.PolicyFunc(func(context.Context, authorization.Context) (authorization.Decision, error) {
		policyCalls++
		if policyCalls > 1 {
			return authorization.Deny("revoked"), nil
		}
		return authorization.Allow(), nil
	}), authorization.PolicyOptions{}))
	evaluator, err := policies.Build(protected.Catalog(), authorization.Options{})
	mustRegister(t, err)
	p = build(t, &protected, queries.PipelineOptions{Authorization: evaluator})
	result, err = p.Perform(identity.WithPrincipal(context.Background(), identity.System()), "Item.Protected", queries.Request{})
	if err != nil || result.IsAuthorized() || policyCalls != 2 {
		t.Fatal("policy verdict cached", err)
	}
}
func TestQueryContextBorrowedScopesExpireAndPreserveSecurityPresence(t *testing.T) {
	var r queries.Registry
	var retained *execution.Scope
	receipt := time.Date(2026, 1, 1, 2, 3, 4, 0, time.UTC)
	id := correlation.FromContext(context.Background())
	mustRegister(t, queries.Register[Item](&r, "Current", queries.Invoke(func(ctx context.Context, inv *queries.Invocation, _ queries.NoArguments) (Item, error) {
		c, ok := queries.ContextFrom(ctx)
		if !ok || c.Name() != "Item.Current" || c.ReceivedAt() != receipt || c.Parameters() != inv.QueryContext().Parameters() {
			t.Fatal("missing query context")
		}
		if _, present := identity.PrincipalFrom(ctx); present {
			t.Fatal("principal absence changed")
		}
		if _, present := tenancy.TenantFrom(ctx); present {
			t.Fatal("tenant absence changed")
		}
		id = c.CorrelationID()
		retained = inv.Scope()
		if err := retained.Close(ctx); !errors.Is(err, execution.ErrScopeView) {
			t.Fatalf("callback closed scope: %v", err)
		}
		return Item{}, nil
	}), public[queries.NoArguments]()))
	p := build(t, &r, queries.PipelineOptions{Clock: func() time.Time { return receipt }})
	holder := &plainResources{}
	scope, err := execution.BorrowScope(context.Background(), holder)
	mustRegister(t, err)
	result, err := p.PerformScoped(context.Background(), scope, "Item.Current", queries.Request{})
	if err != nil || !result.IsSuccess() || id.IsZero() || holder.closes != 0 {
		t.Fatalf("%+v %v", result.Details(), err)
	}
	if err := retained.CheckContext(context.Background()); err == nil {
		t.Fatal("callback view survived")
	}
	changed := identity.WithPrincipal(context.Background(), identity.Principal{})
	result, err = p.PerformScoped(changed, scope, "Item.Current", queries.Request{})
	if err == nil || result.IsSuccess() {
		t.Fatal("security presence mismatch accepted")
	}
	mustRegister(t, scope.Close(context.Background()))
	if holder.closes != 0 {
		t.Fatal("borrowed resources closed")
	}
}
func TestCleanupFailureAndCancellationRetractReadyDataAndPreserveErrors(t *testing.T) {
	for _, cancelDuring := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup", true: "cancellation"}[cancelDuring], func(t *testing.T) {
			var r queries.Registry
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cleanupFailure := errors.New("sensitive cleanup failure")
			holder := &plainResources{closeErr: cleanupFailure}
			mustRegister(t, queries.Register[Item](&r, "Current", queries.Function(func(context.Context, queries.NoArguments) (Item, error) {
				if cancelDuring {
					cancel()
				}
				return Item{Name: "must disappear"}, nil
			}), public[queries.NoArguments]()))
			p := build(t, &r, queries.PipelineOptions{OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
			result, err := p.Perform(ctx, "Item.Current", queries.Request{})
			if !errors.Is(err, cleanupFailure) || holder.closes != 1 || !result.IsReady() || result.IsSuccess() {
				t.Fatalf("result=%+v err=%v closes=%d", result.Details(), err, holder.closes)
			}
			if cancelDuring && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation")
			}
			if _, present := result.Data(); present {
				t.Fatal("failed data present")
			}
			for _, message := range result.Details().ExceptionMessages {
				if strings.Contains(message, "sensitive") || message != "An internal error occurred while processing the request. See server logs for details." {
					t.Fatal(message)
				}
			}
		})
	}
}
func TestPanicSafeFragmentsAndUnexpectedValidatorFailure(t *testing.T) {
	var r queries.Registry
	mustRegister(t, queries.Register[Item](&r, "Panic", queries.Function(func(context.Context, queries.NoArguments) (Item, error) { panic("secret") }), public[queries.NoArguments]()))
	p := build(t, &r, queries.PipelineOptions{})
	result, err := p.Perform(context.Background(), "Item.Panic", queries.Request{})
	var panicError *execution.PanicError
	if !errors.As(err, &panicError) || !result.HasExceptions() || strings.Contains(result.Details().ExceptionMessages[0], "secret") {
		t.Fatalf("%+v %v", result.Details(), err)
	}
	var validationRegistry queries.Registry
	mustRegister(t, queries.Register[Item](&validationRegistry, "Validate", itemPerformer(), public[queries.NoArguments](), queries.WithValidator(validation.ValidatorFunc[queries.NoArguments](func(context.Context, queries.NoArguments) ([]validation.Result, error) {
		return nil, errors.New("secret validator")
	}))))
	p = build(t, &validationRegistry, queries.PipelineOptions{})
	result, err = p.Perform(context.Background(), "Item.Validate", queries.Request{})
	findings := result.Details().ValidationResults
	if err == nil || len(findings) != 1 || findings[0].Reason != validation.ValidatorFailed || findings[0].Message != "The value could not be validated." || result.HasExceptions() {
		t.Fatalf("%+v %v", result.Details(), err)
	}
}
func TestFilterFragmentsCannotPublishDataOrChangesAndZeroDenies(t *testing.T) {
	for _, fragment := range []queries.Result[any]{queries.Result[any]{}, queries.Success[any](correlation.FromContext(context.Background()), Item{}), queries.NewResult[any](queries.Details{Authorized: true, ExceptionMessages: []string{"secret fragment"}}, serialization.Optional[any]{})} {
		var r queries.Registry
		mustRegister(t, queries.Register[Item](&r, "Current", itemPerformer(), public[queries.NoArguments]()))
		mustRegister(t, r.AddFilter("filter", func(context.Context, *execution.Scope) (queries.Filter, error) {
			return queries.FilterFunc(func(context.Context, *queries.Invocation) (queries.Result[any], error) { return fragment, nil }), nil
		}))
		p := build(t, &r, queries.PipelineOptions{})
		result, _ := p.Perform(context.Background(), "Item.Current", queries.Request{})
		if result.IsSuccess() {
			t.Fatal("failed fragment continued")
		}
		if _, present := result.Data(); present {
			t.Fatal("filter data published")
		}
		for _, message := range result.Details().ExceptionMessages {
			if strings.Contains(message, "secret") {
				t.Fatal(message)
			}
		}
	}
}

type resolverResources struct {
	plainResources
	resolves int
}

func (r *resolverResources) Resolve(context.Context, di.Key) (any, error) {
	r.resolves++
	return "dependency", nil
}

type catalog struct{}

func (catalog) Contains(k di.Key) bool { return k == di.KeyFor[string]() }
func TestDIManifestsValidateWithoutResolutionAndManualEquivalence(t *testing.T) {
	makeRegistry := func() *queries.Registry {
		r := &queries.Registry{}
		mustRegister(t, queries.Register[Item](r, "Current", queries.Invoke(func(ctx context.Context, inv *queries.Invocation, _ queries.NoArguments) (Item, error) {
			name, err := execution.Resolve[string](ctx, inv.Scope())
			return Item{Name: name}, err
		}), public[queries.NoArguments](), queries.WithDependencies[queries.NoArguments](di.KeyFor[string]())))
		return r
	}
	r := makeRegistry()
	if _, err := r.Build(queries.PipelineOptions{}); err == nil {
		t.Fatal("missing catalog accepted")
	}
	holder := &resolverResources{}
	p := build(t, r, queries.PipelineOptions{DependencyCatalog: catalog{}, OpenResources: func(context.Context) (execution.Resources, error) { return holder, nil }})
	if holder.resolves != 0 {
		t.Fatal("Build resolved dependencies")
	}
	result, err := queries.Perform[Item](context.Background(), p, "Item.Current", queries.Request{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := result.Data()
	if data.Name != "dependency" || holder.resolves != 1 || holder.closes != 1 {
		t.Fatalf("data=%+v holder=%+v", data, holder)
	}
}
