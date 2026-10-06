// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

type pipelineCommand struct{}
type pipelineStore struct {
	closes   int
	closeErr error
}

func (s *pipelineStore) Close(context.Context) error { s.closes++; return s.closeErr }
func (*pipelineStore) Read() row                     { return row{"a1", "Ada"} }

type pipelineFixture struct {
	commands      commands.Pipeline
	queries       queries.Pipeline
	store         *pipelineStore
	queryCalls    int
	commandCalls  int
	policyCalls   int
	opens         int
	retained      *execution.Scope
	retainedQuery *execution.Scope
}

func pipelineMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pipelineContext(t *testing.T) context.Context {
	t.Helper()
	id, err := correlation.Parse("00112233-4455-4677-8899-aabbccddeeff")
	pipelineMust(t, err)
	return correlation.WithID(tenancy.WithTenant(identity.WithPrincipal(t.Context(), identity.System("Editor")), tenancy.Default()), id)
}

// Each composition mode uses the same business methods and shared evaluator.
// Only dependency acquisition changes; the container remains a test-only import.
func newPipelineFixture(t *testing.T, mode string, closeErr error) *pipelineFixture {
	t.Helper()
	f := &pipelineFixture{store: &pipelineStore{closeErr: closeErr}}
	commandOptions := commands.PipelineOptions{RequireTenant: true}
	queryOptions := queries.PipelineOptions{RequireTenant: true}
	resolve := func(ctx context.Context, scope *execution.Scope) (*pipelineStore, error) {
		return f.store, scope.CheckContext(ctx)
	}
	var keys []di.Key
	switch mode {
	case "manual":
	case "resources":
		open := func(context.Context) (execution.Resources, error) { f.opens++; return f.store, nil }
		commandOptions.OpenResources, queryOptions.OpenResources = open, open
		resolve = execution.ResourcesAs[*pipelineStore]
	case "container":
		var registry container.Registry
		pipelineMust(t, di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (*pipelineStore, error) {
			f.opens++
			return f.store, nil
		}))
		provider, err := registry.Build(container.WithContextGuard(execution.ContextGuard()))
		pipelineMust(t, err)
		t.Cleanup(func() { pipelineMust(t, provider.Close(context.Background())) })
		commandOptions.ScopeFactory, queryOptions.ScopeFactory = provider, provider
		resolve = execution.Resolve[*pipelineStore]
		keys = []di.Key{di.KeyFor[*pipelineStore]()}
	default:
		t.Fatalf("unknown composition mode %q", mode)
	}
	receipt := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	commandOptions.Clock = func() time.Time { return receipt }
	queryOptions.Clock = func() time.Time { return receipt.Add(time.Second) }
	declaration := metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Editor"}, Policy: "shared"}}}
	var cr commands.Registry
	var qr queries.Registry
	pipelineMust(t, queries.Register[row](&qr, "Read", queries.Invoke(func(ctx context.Context, inv *queries.Invocation, _ queries.NoArguments) (row, error) {
		f.queryCalls++
		f.retainedQuery = inv.Scope()
		qc := inv.QueryContext()
		cc, ok := commands.ContextFrom(ctx)
		if !ok || qc.CorrelationID() != cc.CorrelationID() || !qc.Principal().Equal(cc.Principal()) || qc.Tenant() != cc.Tenant() || !qc.ReceivedAt().Equal(receipt.Add(time.Second)) || !cc.ReceivedAt().Equal(receipt) {
			t.Error("nested query lost security, correlation, or distinct receipt metadata")
		}
		store, err := resolve(ctx, inv.Scope())
		if err != nil {
			return row{}, err
		}
		if store.closes != 0 {
			t.Error("resource disposed before nested query")
		}
		return store.Read(), nil
	}), queries.WithAuthorization[queries.NoArguments](declaration), queries.WithDependencies[queries.NoArguments](keys...)))
	pipelineMust(t, commands.Register[pipelineCommand](&cr, commands.Invoke(func(ctx context.Context, inv *commands.Invocation, _ pipelineCommand) (row, error) {
		f.commandCalls++
		f.retained = inv.Scope()
		store, err := resolve(ctx, inv.Scope())
		if err != nil {
			return row{}, err
		}
		query, err := f.queries.PerformScoped(ctx, inv.Scope(), "row.Read", queries.RequestFor(queries.NoArguments{}, queries.Parameters{}))
		if err != nil {
			return row{}, err
		}
		value, present := query.Data()
		if !query.IsSuccess() || !present || value != store.Read() || store.closes != 0 || query.Details().CorrelationID != inv.CommandContext().CorrelationID() {
			t.Fatalf("nested query = %#v, %v", query.Details(), value)
		}
		return value.(row), nil
	}), commands.WithAuthorization[pipelineCommand](declaration), commands.WithHandlingDependencies[pipelineCommand](keys...)))
	catalog := cr.Catalog()
	catalog.Queries = qr.Catalog().Queries
	var ar authorization.Registry
	pipelineMust(t, authorization.RegisterPolicy(&ar, "shared", func(ctx context.Context, scope *execution.Scope) (authorization.PolicyFunc, error) {
		f.policyCalls++
		if err := scope.CheckContext(ctx); err != nil {
			return nil, err
		}
		return func(_ context.Context, value authorization.Context) (authorization.Decision, error) {
			if value.Tenant != tenancy.Default() || !value.Principal.HasRole("Editor") {
				t.Error("policy lost operation security")
			}
			return authorization.Allow(), nil
		}, nil
	}, authorization.PolicyOptions{}))
	evaluator, err := ar.Build(catalog, authorization.Options{})
	pipelineMust(t, err)
	commandOptions.Authorization, queryOptions.Authorization = evaluator, evaluator
	f.queries, err = qr.Build(queryOptions)
	pipelineMust(t, err)
	f.commands, err = cr.Build(commandOptions)
	pipelineMust(t, err)
	if f.opens != 0 || f.commandCalls != 0 || f.queryCalls != 0 || f.policyCalls != 0 {
		t.Fatal("Build activated application code")
	}
	return f
}

func TestCommandQuerySharedScopeCompositionEquivalence(t *testing.T) {
	var reference []byte
	for _, mode := range []string{"manual", "resources", "container"} {
		t.Run(mode, func(t *testing.T) {
			f := newPipelineFixture(t, mode, nil)
			ctx := pipelineContext(t)
			result, err := f.commands.Execute(ctx, pipelineCommand{})
			pipelineMust(t, err)
			value, present := result.Response()
			if !result.IsSuccess() || !present || value != (row{"a1", "Ada"}) || result.Details().CorrelationID != correlation.FromContext(ctx) || f.commandCalls != 1 || f.queryCalls != 1 || f.policyCalls < 2 {
				t.Fatalf("result = %#v, response = %v; calls = %d/%d/%d", result.Details(), value, f.commandCalls, f.queryCalls, f.policyCalls)
			}
			wantCloses := 1
			if mode == "manual" {
				wantCloses = 0
			}
			if f.store.closes != wantCloses || (mode != "manual" && f.opens != 1) {
				t.Fatalf("opens/closes = %d/%d", f.opens, f.store.closes)
			}
			if !errors.Is(f.retained.CheckContext(ctx), execution.ErrScopeExpired) || !errors.Is(f.retainedQuery.CheckContext(ctx), execution.ErrScopeExpired) {
				t.Fatal("pipeline callback scopes survived their stages")
			}
			body := pipelineJSON(t, result)
			if reference == nil {
				reference = body
			} else if !reflect.DeepEqual(body, reference) {
				t.Fatalf("composition changed wire envelope: %s != %s", body, reference)
			}
		})
	}
}

func TestCombinedEvaluatorRejectsChangedDeclarationsInBothLanes(t *testing.T) {
	var cr commands.Registry
	var qr queries.Registry
	pipelineMust(t, commands.Register[pipelineCommand](&cr, commands.Void(func(pipelineCommand, context.Context) error { return nil })))
	pipelineMust(t, queries.Register[row](&qr, "Read", queries.Function(func(context.Context, queries.NoArguments) (row, error) { return row{}, nil })))
	catalog := cr.Catalog()
	catalog.Queries = qr.Catalog().Queries
	catalog.Commands[0].Authorization = &metadata.Authorization{AllowAnonymous: true}
	catalog.Queries[0].Authorization = &metadata.Authorization{AllowAnonymous: true}
	var ar authorization.Registry
	evaluator, err := ar.Build(catalog, authorization.Options{})
	pipelineMust(t, err)
	if _, err := cr.Build(commands.PipelineOptions{Authorization: evaluator}); !errors.Is(err, authorization.ErrCatalogMismatch) {
		t.Fatalf("command mismatch = %v", err)
	}
	if _, err := qr.Build(queries.PipelineOptions{Authorization: evaluator}); !errors.Is(err, authorization.ErrCatalogMismatch) {
		t.Fatalf("query mismatch = %v", err)
	}
	// Failed Build must leave both registries editable/retryable.
	_, err = cr.Build(commands.PipelineOptions{})
	pipelineMust(t, err)
	_, err = qr.Build(queries.PipelineOptions{})
	pipelineMust(t, err)
}
