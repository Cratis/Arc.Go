// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer

import (
	"context"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/queries"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

func TestGenerated(t *testing.T) {
	for _, useContainer := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "container"}[useContainer], func(t *testing.T) {
			resolved := 0
			write := writer(func(_ context.Context, name string) (int, error) { return len(name), nil })
			options := arc.Options{}
			var supplied []ArcBindings
			if useContainer {
				var registry container.Registry
				if err := di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (writer, error) { resolved++; return write, nil }); err != nil {
					t.Fatal(err)
				}
				provider, err := registry.Build()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := provider.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				options.ScopeFactory = provider
			} else {
				supplied = []ArcBindings{{ResolveWriter: func(context.Context, *execution.Scope) (writer, error) { resolved++; return write, nil }}}
			}
			builder, err := arc.NewBuilder(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := RegisterArtifacts(builder, supplied...); err != nil {
				t.Fatal(err)
			}
			app, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			if resolved != 0 {
				t.Fatal("eager resolution")
			}
			if err := app.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := app.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			for _, command := range []any{add{}, singular{}, resultControl{}} {
				result, err := app.Commands().Execute(t.Context(), command)
				if err != nil || result.IsSuccess() || resolved != 0 {
					t.Fatal("rejection constructed dependency", err)
				}
			}
			validated, err := app.Commands().Validate(t.Context(), add{Name: "valid"})
			if err != nil || !validated.IsSuccess() || resolved != 0 {
				t.Fatal("validate-only resolved writer", err)
			}
			result, err := app.Commands().Execute(t.Context(), add{Name: "four"})
			value, present := result.Response()
			if err != nil || !result.IsSuccess() || !present || value != 4 || resolved != 1 {
				t.Fatal("generated command failed", err, result.Details())
			}
			ignoredPreparation, err := app.Commands().Execute(t.Context(), bindings{Code: 7})
			if err != nil || !ignoredPreparation.IsSuccess() {
				t.Fatal("ignored Provide was invoked", err)
			}
			ctx := identity.WithPrincipal(t.Context(), identity.System("Reader"))
			for _, name := range []queries.FullyQualifiedQueryName{"Item.All", "Item.Private", "Item.Recent", "Item.Array"} {
				query, err := app.Queries().Perform(ctx, name, queries.RequestFor(queries.NoArguments{}, queries.Parameters{}))
				if err != nil || !query.IsSuccess() {
					t.Fatal("generated query failed", name, err, query.Details())
				}
			}
			if len(app.Catalog().Queries) != 4 {
				t.Fatal("discovered unrelated/stateful/ignored methods")
			}
		})
	}
}
