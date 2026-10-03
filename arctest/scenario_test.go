// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arctest_test

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/arctest"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
)

type rename struct {
	Name string `json:"name" validate:"required"`
}
type person struct {
	Name string `json:"name"`
}
type selection struct {
	Prefix string `json:"prefix"`
}
type resources struct{ steps *[]string }

func (r *resources) Close(context.Context) error { *r.steps = append(*r.steps, "close"); return nil }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func builderFor(t *testing.T, options arc.Options) *arc.Builder {
	t.Helper()
	builder, err := arc.NewBuilder(options)
	must(t, err)
	return builder
}

func TestCommandScenarioValidatesWithoutPreparingAndClosesResources(t *testing.T) {
	var steps []string
	builder := builderFor(t, arc.Options{OpenResources: func(context.Context) (execution.Resources, error) {
		steps = append(steps, "open")
		return &resources{&steps}, nil
	}})
	must(t, commands.Register(builder, commands.WithProvide(
		func(c rename, _ context.Context) (string, error) {
			steps = append(steps, "provide")
			return c.Name, nil
		},
		func(_ rename, _ context.Context, name string) (string, error) {
			steps = append(steps, "handle")
			return name, nil
		},
	)))
	scenario := arctest.NewCommand[rename, string](arctest.New(t, builder))
	validated, err := scenario.Validate(t.Context(), rename{Name: "Ada"})
	arctest.RequireNoResponse(t, validated, err)
	if !reflect.DeepEqual(steps, []string{"open", "close"}) {
		t.Fatalf("validate steps = %v", steps)
	}
	steps = nil
	result, err := scenario.Execute(t.Context(), rename{Name: "Ada"})
	if got := arctest.RequireResponse(t, result, err); got != "Ada" {
		t.Fatalf("response = %q", got)
	}
	if !reflect.DeepEqual(steps, []string{"open", "provide", "handle", "close"}) {
		t.Fatalf("execute steps = %v", steps)
	}
	steps = nil
	invalid, _ := scenario.Execute(t.Context(), rename{})
	arctest.RequireValidationErrors(t, invalid.Details().ValidationResults)
	if _, present := invalid.Response(); present {
		t.Fatal("failed result retained response")
	}
	if !reflect.DeepEqual(steps, []string{"open", "close"}) {
		t.Fatalf("invalid steps = %v", steps)
	}
}

func TestCommandScenarioPreservesTrustedMetadataAndAuthorization(t *testing.T) {
	receipt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	builder := builderFor(t, arc.Options{Clock: func() time.Time { return receipt }})
	var captured execution.Metadata
	must(t, commands.Register(builder, commands.Handle(func(_ rename, ctx context.Context) (bool, error) {
		captured = execution.Capture(ctx)
		return false, nil
	}), commands.WithAuthorization[rename](metadata.Authorization{})))
	scenario := arctest.NewCommand[rename, bool](arctest.New(t, builder))
	denied, _ := scenario.Execute(t.Context(), rename{Name: "Ada"})
	arctest.RequireUnauthorized(t, denied)
	principal := identity.System("writers")
	ctx, err := execution.NewContext(t.Context(), execution.Metadata{Principal: principal, Tenant: tenancy.Default(), ReceivedAt: receipt})
	must(t, err)
	result, err := scenario.Execute(ctx, rename{Name: "Ada"})
	if arctest.RequireResponse(t, result, err) {
		t.Fatal("zero response changed")
	}
	if !captured.Principal.Equal(principal) || captured.Tenant != tenancy.Default() || !captured.ReceivedAt.Equal(receipt) || captured.CorrelationID != result.Details().CorrelationID {
		t.Fatalf("metadata lost: %+v", captured)
	}
}

func TestCommandScenarioPreservesErrorsCancellationAndStoppedState(t *testing.T) {
	failure := errors.New("repository unavailable")
	builder := builderFor(t, arc.Options{})
	calls := 0
	must(t, commands.Register(builder, commands.Handle(func(rename, context.Context) (string, error) { calls++; return "", failure })))
	fixture := arctest.New(t, builder)
	scenario := arctest.NewCommand[rename, string](fixture)
	result, err := scenario.Execute(t.Context(), rename{Name: "Ada"})
	if !errors.Is(err, failure) || !result.HasExceptions() {
		t.Fatalf("result/error = %+v / %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = scenario.Execute(ctx, rename{Name: "Ada"})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("canceled error/calls = %v / %d", err, calls)
	}
	must(t, fixture.Close(t.Context()))
	must(t, fixture.Close(t.Context()))
	_, err = scenario.Execute(t.Context(), rename{Name: "Ada"})
	if !errors.Is(err, arc.ErrStopped) {
		t.Fatalf("after close = %v", err)
	}
}

func TestQueryScenarioBindsRendersInterceptsAndReleasesScope(t *testing.T) {
	var steps []string
	builder := builderFor(t, arc.Options{OpenResources: func(context.Context) (execution.Resources, error) {
		steps = append(steps, "open")
		return &resources{&steps}, nil
	}})
	must(t, queries.Register[person](builder, "All", queries.Function(func(_ context.Context, args selection) ([]person, error) {
		steps = append(steps, "perform")
		return []person{{Name: args.Prefix + "Ada"}}, nil
	})))
	must(t, queries.RegisterReadModelInterceptor(builder.Queries(), "mask", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[person], error) {
		return queries.InterceptorFunc[person](func(_ context.Context, p person) (person, error) {
			steps = append(steps, "intercept")
			p.Name += "!"
			return p, nil
		}), nil
	}))
	scenario := arctest.NewQuery[[]person](arctest.New(t, builder), "person.All")
	request, err := queries.ReadGET(url.Values{"prefix": {"Dr "}})
	must(t, err)
	result, err := scenario.Perform(t.Context(), request)
	if got := arctest.RequireData(t, result, err); !reflect.DeepEqual(got, []person{{Name: "Dr Ada!"}}) {
		t.Fatalf("data = %+v", got)
	}
	if !reflect.DeepEqual(steps, []string{"open", "perform", "intercept", "close"}) {
		t.Fatalf("steps = %v", steps)
	}
}

func TestQueryScenarioReturnsFailureRatherThanEmptySuccess(t *testing.T) {
	builder := builderFor(t, arc.Options{})
	must(t, queries.Register[person](builder, "All", queries.Function(func(context.Context, queries.NoArguments) ([]person, error) { return []person{}, nil })))
	fixture := arctest.New(t, builder)
	scenario := arctest.NewQuery[[]person](fixture, "person.Missing")
	result, err := scenario.Perform(t.Context(), queries.Request{})
	if err == nil || result.IsSuccess() {
		t.Fatalf("unknown query = %+v / %v", result, err)
	}
	must(t, fixture.Close(t.Context()))
	_, err = arctest.NewQuery[[]person](fixture, "person.All").Perform(t.Context(), queries.Request{})
	if !errors.Is(err, arc.ErrStopped) {
		t.Fatalf("after close = %v", err)
	}
}

func TestScenarioOwnsApplicationCleanup(t *testing.T) {
	hook := &lifecycleProbe{}
	t.Run("owner", func(t *testing.T) {
		builder := builderFor(t, arc.Options{})
		must(t, builder.AddLifecycle("probe", hook))
		arctest.New(t, builder)
		if hook.started != 1 || hook.stopped != 0 {
			t.Fatalf("premature lifecycle = %+v", hook)
		}
	})
	if hook.stopped != 1 {
		t.Fatalf("cleanup did not stop application: %+v", hook)
	}
}

type lifecycleProbe struct{ started, stopped int }

func (p *lifecycleProbe) Start(context.Context) error { p.started++; return nil }
func (p *lifecycleProbe) Stop(context.Context) error  { p.stopped++; return nil }

func TestValidationReasonHelpersAcceptExplicitDependencyFailure(t *testing.T) {
	arctest.RequireValidationReason(t, []validation.Result{{Reason: validation.DependencyUnavailable}}, validation.DependencyUnavailable)
}
