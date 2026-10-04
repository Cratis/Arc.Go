// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package sdk_test

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"os"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/integrations/chronicle/sdk"
	"github.com/cratis/chronicle.go"
	"github.com/cratis/chronicle.go/seeding"
	"github.com/cratis/chronicle.go/services"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

type exampleCompositionSeeder struct{ client *chronicle.Client }

func (*exampleCompositionSeeder) Seed(*seeding.Builder) error { return nil }

func ExampleNew_sharedProvider() {
	run := func(ctx context.Context) (err error) {
		registry := chronicle.NewRegistry()
		if _, err := chronicle.RegisterEvent[compositionEvent](registry); err != nil {
			return err
		}
		if err := chronicle.RegisterSeederFactory[*exampleCompositionSeeder](registry, nil); err != nil {
			return err
		}
		// begin-shared-provider
		preparation, err := chronicle.CaptureClient(
			chronicle.WithRegistry(registry),
			chronicle.WithAppendOriginResolver(sdk.ResolveAppendOrigin),
		)
		if err != nil {
			return err
		}
		var provider di.Provider
		defer func() {
			// No work/observers in this setup example. Runtime users drain Arc first.
			err = errors.Join(err, preparation.Client().Close())
			if provider != nil {
				err = errors.Join(err, provider.Close(ctx))
			}
		}()
		var bindings container.Registry
		if err := di.BindValue(&bindings, preparation.Client()); err != nil {
			return err
		}
		if err := di.BindFunc1(&bindings, di.Singleton, func(_ context.Context, client *chronicle.Client) (*exampleCompositionSeeder, error) {
			return &exampleCompositionSeeder{client: client}, nil
		}); err != nil {
			return err
		}
		provider, err = bindings.Build()
		if err != nil {
			return err
		}
		client, err := services.PrepareClient(ctx, preparation, provider)
		if err != nil {
			return err
		}
		adapter, err := sdk.New(client, sdk.Config{Store: "example", OwnClient: false})
		if err != nil {
			return err
		}
		builder, err := arc.NewBuilder(arc.Options{ScopeFactory: provider, DependencyCatalog: provider})
		if err != nil {
			return err
		}
		if err := adapter.Install(builder); err != nil {
			return err
		}
		// end-shared-provider
		if err := commands.Register[compositionCommand](builder, commands.Handle(func(compositionCommand, context.Context) (commands.NoResponse, error) {
			return commands.NoResponse{}, nil
		})); err != nil {
			return err
		}
		app, err := builder.Build()
		if err != nil {
			return err
		}
		if err := app.Start(ctx); err != nil {
			return err
		}
		// No Execute, Connect, registration, observer attachment or catch-up here.
		return errors.Join(app.Shutdown(ctx), adapter.Close())
	}
	if err := run(context.Background()); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("prepared without connecting")
	// Output: prepared without connecting
}

func TestCompositionDocumentationMatchesCompiledSequence(t *testing.T) {
	source, err := os.ReadFile("composition_example_test.go")
	compositionRequire(t, err)
	document, err := os.ReadFile("../../../Documentation/backend/go/chronicle/index.md")
	compositionRequire(t, err)
	_, sourceBlock, found := strings.Cut(string(source), "// begin-shared-provider\n")
	if !found {
		t.Fatal("missing example start marker")
	}
	sourceBlock, _, found = strings.Cut(sourceBlock, "// end-shared-provider")
	if !found {
		t.Fatal("missing example end marker")
	}
	_, docBlock, found := strings.Cut(string(document), "<!-- shared-provider-sequence -->\n\n```go\n")
	if !found {
		t.Fatal("missing shared-provider documentation block")
	}
	docBlock, _, found = strings.Cut(docBlock, "\n```")
	if !found {
		t.Fatal("missing documentation code fence")
	}
	normalize := func(block string) string {
		body, err := format.Source([]byte("package example\nfunc excerpt() error {\n" + strings.TrimSpace(block) + "\nreturn nil\n}\n"))
		compositionRequire(t, err)
		return string(body)
	}
	got, want := normalize(docBlock), normalize(sourceBlock)
	if got != want {
		t.Fatalf("shared-provider sequence differs from compiled ExampleNew_sharedProvider:\nDOC:\n%s\nSOURCE:\n%s", got, want)
	}
}
