// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/queries"
)

type generatedCommand struct{}
type generatedModel struct{}

func TestGeneratedRegistrationOptionsPreserveNamespaceDefaults(t *testing.T) {
	builder, err := arc.NewBuilder(arc.Options{Namespace: "Shop.Tasks"})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[generatedCommand](builder,
		commands.WithName[generatedCommand]("Register"),
		commands.WithExcludeFromDiscovery[generatedCommand](true),
		commands.Void(func(generatedCommand, context.Context) error { return nil })); err != nil {
		t.Fatal(err)
	}
	if err := queries.RegisterReadModel[generatedModel](builder,
		queries.WithModelName("Detail"), queries.WithModelExcludeFromDiscovery(true)); err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[generatedModel](builder, "All",
		queries.Function(func(context.Context, queries.NoArguments) ([]generatedModel, error) { return nil, nil })); err != nil {
		t.Fatal(err)
	}
	catalog := builder.Catalog()
	if got := catalog.Commands[0]; got.Type.Identity() != "Shop.Tasks.Register" || !got.ExcludeFromDiscovery {
		t.Fatal("command overrides lost defaults", got)
	}
	if got := catalog.Queries[0]; got.Identity() != "Shop.Tasks.Detail.All" || !got.ExcludeFromDiscovery {
		t.Fatal("model overrides lost defaults", got)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatal(err)
	}
}
