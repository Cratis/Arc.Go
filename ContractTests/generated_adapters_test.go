// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	arc "github.com/cratis/arc.go"
	fixture "github.com/cratis/arc.go/ContractTests/generatedconsumer"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

type generatedServices struct {
	readers, writers, queries int
	loads, writes, reads      int
	fail                      error
}

func (s *generatedServices) Load(_ context.Context, name string) (fixture.State, error) {
	s.loads++
	return fixture.State{Name: name}, s.fail
}
func (s *generatedServices) Write(_ context.Context, state fixture.State) (string, error) {
	s.writes++
	return state.Name, nil
}
func (s *generatedServices) All(_ context.Context, prefix string) ([]fixture.Item, error) {
	s.reads++
	return []fixture.Item{{ID: "1", Name: prefix}}, nil
}

func generatedApplication(t *testing.T, generated, withContainer bool) (*arc.Application, *generatedServices) {
	t.Helper()
	services := &generatedServices{}
	options := arc.Options{}
	bindings := fixture.ArcBindings{
		ResolveReader: func(ctx context.Context, scope *execution.Scope) (fixture.Reader, error) {
			services.readers++
			return services, scope.CheckContext(ctx)
		},
		ResolveWriter: func(ctx context.Context, scope *execution.Scope) (fixture.Writer, error) {
			services.writers++
			return services, scope.CheckContext(ctx)
		},
		ResolveItemQueries: func(ctx context.Context, scope *execution.Scope) (fixture.ItemQueries, error) {
			services.queries++
			return services, scope.CheckContext(ctx)
		},
	}
	var readerKeys, writerKeys, queryKeys []di.Key
	if withContainer {
		var registry container.Registry
		pipelineMust(t, di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (fixture.Reader, error) { services.readers++; return services, nil }))
		pipelineMust(t, di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (fixture.Writer, error) { services.writers++; return services, nil }))
		pipelineMust(t, di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (fixture.ItemQueries, error) {
			services.queries++
			return services, nil
		}))
		provider, err := registry.Build(container.WithContextGuard(execution.ContextGuard()))
		pipelineMust(t, err)
		t.Cleanup(func() { pipelineMust(t, provider.Close(context.Background())) })
		options.ScopeFactory = provider
		bindings = fixture.ArcBindings{ResolveReader: execution.Resolve[fixture.Reader], ResolveWriter: execution.Resolve[fixture.Writer], ResolveItemQueries: execution.Resolve[fixture.ItemQueries]}
		readerKeys = []di.Key{di.KeyFor[fixture.Reader]()}
		writerKeys = []di.Key{di.KeyFor[fixture.Writer]()}
		queryKeys = []di.Key{di.KeyFor[fixture.ItemQueries]()}
	}
	builder, err := arc.NewBuilder(options)
	pipelineMust(t, err)
	if generated {
		if withContainer {
			pipelineMust(t, fixture.RegisterArtifacts(builder))
		} else {
			pipelineMust(t, fixture.RegisterArtifacts(builder, bindings))
		}
	} else {
		declaration := metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Editor", "Admin"}}, {Roles: []string{"Verified"}}}}
		pipelineMust(t, commands.Register[fixture.AddItem](builder, commands.Prepare(
			func(ctx context.Context, inv *commands.Invocation, c fixture.AddItem) (commands.Preparation[fixture.State], error) {
				reader, err := bindings.ResolveReader(ctx, inv.Scope())
				if err != nil {
					return commands.Preparation[fixture.State]{}, err
				}
				return c.Provide(ctx, reader, inv.CommandContext())
			}, func(ctx context.Context, inv *commands.Invocation, c fixture.AddItem, state fixture.State) (string, error) {
				writer, err := bindings.ResolveWriter(ctx, inv.Scope())
				if err != nil {
					return "", err
				}
				return c.Handle(ctx, state, writer)
			}), commands.WithDescriptor[fixture.AddItem](metadata.Command{Type: metadata.TypeName{Namespace: "Generated.Shop", Name: "AddItem"}, Authorization: &declaration, ExcludeFromDiscovery: true}), commands.WithBlockOnValidationSeverity[fixture.AddItem](validation.Warning), commands.WithPreparationDependencies[fixture.AddItem](readerKeys...), commands.WithHandlingDependencies[fixture.AddItem](writerKeys...)))
		pipelineMust(t, queries.RegisterReadModel[fixture.Item](builder, queries.WithModelIdentity(metadata.TypeName{Namespace: "Generated.Shop", Name: "Item"}), queries.WithModelAuthorization(metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Reader"}}}})))
		pipelineMust(t, queries.Register[fixture.Item](builder, "AllItems", queries.Invoke(func(ctx context.Context, inv *queries.Invocation, args fixture.Arguments) ([]fixture.Item, error) {
			items, err := bindings.ResolveItemQueries(ctx, inv.Scope())
			if err != nil {
				return nil, err
			}
			return (fixture.Item{}).AllItems(ctx, args, items, inv.QueryContext(), inv.QueryContext().Parameters())
		}), queries.WithDependencies[fixture.Arguments](queryKeys...)))
		pipelineMust(t, queries.Register[fixture.Item](builder, "Recent", queries.Invoke(func(ctx context.Context, inv *queries.Invocation, args fixture.Arguments) ([]fixture.Item, error) {
			items, err := bindings.ResolveItemQueries(ctx, inv.Scope())
			if err != nil {
				return nil, err
			}
			return fixture.RecentItems(ctx, args, items)
		}), queries.WithAuthorization[fixture.Arguments](metadata.Authorization{AllowAnonymous: true}), queries.WithPath[fixture.Arguments]("/generated/recent"), queries.WithHTTPMethod[fixture.Arguments](metadata.QueryHTTPQuery), queries.WithDependencies[fixture.Arguments](queryKeys...)))
	}
	app, err := builder.Build()
	pipelineMust(t, err)
	if services.readers+services.writers+services.queries != 0 {
		t.Fatal("registration/Build resolved dependencies")
	}
	pipelineMust(t, app.Start(t.Context()))
	t.Cleanup(func() { pipelineMust(t, app.Shutdown(context.Background())) })
	return app, services
}

func TestGeneratedAndManualAdaptersHaveEquivalentStaging(t *testing.T) {
	for _, generated := range []bool{false, true} {
		for _, withContainer := range []bool{false, true} {
			name := map[bool]string{true: "generated", false: "manual"}[generated] + "/" + map[bool]string{true: "container", false: "plain"}[withContainer]
			t.Run(name, func(t *testing.T) {
				app, s := generatedApplication(t, generated, withContainer)
				ctx := identity.WithPrincipal(pipelineContext(t), identity.System("Editor", "Verified", "Reader"))
				denied, err := app.Commands().Execute(pipelineContext(t), fixture.AddItem{Name: "denied"})
				pipelineMust(t, err)
				if denied.IsAuthorized() || s.readers+s.writers != 0 {
					t.Fatal("missing conjunctive role did not suppress dependencies")
				}
				validated, err := app.Commands().Validate(ctx, fixture.AddItem{Name: "valid"})
				pipelineMust(t, err)
				if !validated.IsSuccess() || s.readers+s.writers != 0 {
					t.Fatal("validate-only resolved dependencies")
				}
				invalid, err := app.Commands().Execute(ctx, fixture.AddItem{Name: "invalid"})
				pipelineMust(t, err)
				if invalid.IsValid() || s.readers+s.writers != 0 {
					t.Fatal("model validation did not suppress dependencies")
				}
				stopped, err := app.Commands().Execute(ctx, fixture.AddItem{Name: "stop", Stop: true})
				pipelineMust(t, err)
				if !stopped.IsSuccess() || s.readers != 1 || s.writers != 0 || s.loads != 0 {
					t.Fatal("stopped Provide resolved Handle dependency")
				}
				s.fail = errors.New("reader unavailable")
				failed, err := app.Commands().Execute(ctx, fixture.AddItem{Name: "failure"})
				if !errors.Is(err, s.fail) || failed.IsSuccess() || s.writers != 0 {
					t.Fatal("failed Provide resolved Handle dependency", err)
				}
				s.fail = nil
				result, err := app.Commands().Execute(ctx, fixture.AddItem{Name: "created"})
				pipelineMust(t, err)
				response, present := result.Response()
				if !result.IsSuccess() || !present || response != "created" || s.readers != 3 || s.writers != 1 || s.writes != 1 {
					t.Fatalf("unexpected command result: %#v, %#v", result.Details(), s)
				}
				request := queries.RequestFor(fixture.Arguments{Prefix: "name"}, queries.Parameters{})
				for _, queryName := range []queries.FullyQualifiedQueryName{"Generated.Shop.Item.AllItems", "Generated.Shop.Item.Recent"} {
					q, err := app.Queries().Perform(ctx, queryName, request)
					pipelineMust(t, err)
					data, present := q.Data()
					if !q.IsSuccess() || !present || !reflect.DeepEqual(data, []fixture.Item{{ID: "1", Name: "name"}}) {
						t.Fatalf("query %s: %#v", queryName, q.Details())
					}
				}
				deniedQuery, err := app.Queries().Perform(t.Context(), "Generated.Shop.Item.AllItems", request)
				pipelineMust(t, err)
				if deniedQuery.IsAuthorized() || s.queries != 2 {
					t.Fatal("denied query resolved services")
				}
				publicQuery, err := app.Queries().Perform(t.Context(), "Generated.Shop.Item.Recent", request)
				pipelineMust(t, err)
				if !publicQuery.IsSuccess() || s.queries != 3 {
					t.Fatal("query declaration did not replace model authorization")
				}
				badQuery, err := app.Queries().Perform(ctx, "Generated.Shop.Item.AllItems", queries.NewRequest(queries.Arguments{}, queries.Parameters{}))
				var argumentError *queries.ArgumentError
				if !errors.As(err, &argumentError) || !argumentError.Missing || badQuery.IsValid() || s.queries != 3 {
					t.Fatal("invalid query resolved services")
				}
			})
		}
	}
}

func TestGeneratedMetadataMatchesManualMetadata(t *testing.T) {
	manual, _ := generatedApplication(t, false, false)
	generated, _ := generatedApplication(t, true, false)
	mc, ok := manual.Commands().Lookup("Generated.Shop.AddItem")
	if !ok {
		t.Fatal("missing manual command")
	}
	gc, ok := generated.Commands().Lookup("Generated.Shop.AddItem")
	if !ok {
		t.Fatal("missing generated command")
	}
	if !reflect.DeepEqual(mc.Descriptor(), gc.Descriptor()) {
		t.Fatalf("command metadata mismatch: %#v / %#v", mc.Descriptor(), gc.Descriptor())
	}
	for _, name := range []queries.FullyQualifiedQueryName{"Generated.Shop.Item.AllItems", "Generated.Shop.Item.Recent"} {
		mq, mok := manual.Queries().Lookup(name)
		gq, gok := generated.Queries().Lookup(name)
		if !mok || !gok || !reflect.DeepEqual(mq.Descriptor(), gq.Descriptor()) || !reflect.DeepEqual(mq.Parameters(), gq.Parameters()) {
			t.Fatalf("query metadata mismatch: %#v / %#v", mq.Descriptor(), gq.Descriptor())
		}
	}
}

func TestGeneratedPreparationControlsAndPointerCommands(t *testing.T) {
	for _, withContainer := range []bool{false, true} {
		t.Run(map[bool]string{true: "container", false: "plain"}[withContainer], func(t *testing.T) {
			app, s := generatedApplication(t, true, withContainer)
			for _, command := range []any{fixture.Checked{}, fixture.Guarded{}} {
				result, err := app.Commands().Execute(t.Context(), command)
				pipelineMust(t, err)
				if result.IsSuccess() || s.writers != 0 {
					t.Fatalf("control did not block: %#v", result.Details())
				}
			}
			warning, err := app.Commands().Execute(t.Context(), fixture.Checked{Warning: true})
			pipelineMust(t, err)
			if !warning.IsSuccess() || s.writers != 1 {
				t.Fatal("nonblocking preparation warning did not continue")
			}
			rename, err := app.Commands().Execute(t.Context(), fixture.Rename{Name: "prepared"})
			pipelineMust(t, err)
			value, present := rename.Response()
			if !rename.IsSuccess() || !present || value != "prepared" {
				t.Fatal("ordinary Provide handoff failed")
			}
			reset, err := app.Commands().Execute(t.Context(), &fixture.Reset{})
			pipelineMust(t, err)
			if !reset.IsSuccess() {
				t.Fatal("pointer error-only Handle failed")
			}
		})
	}
}

func TestGeneratedDefaultBindingsRequireDependencyCatalog(t *testing.T) {
	builder, err := arc.NewBuilder(arc.Options{})
	pipelineMust(t, err)
	pipelineMust(t, fixture.RegisterArtifacts(builder))
	if _, err := builder.Build(); err == nil {
		t.Fatal("default resolver dependencies were not declared")
	}
	if err := fixture.RegisterArtifacts(nil); err == nil {
		t.Fatal("nil builder accepted")
	}
}
