package arc_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

type builderCommand struct{}

func (builderCommand) Handle(context.Context) error { return nil }

type builderModel struct{ Name string }

func TestBuilderCombinedFrozenComposition(t *testing.T) {
	routes := metadata.DefaultOptions()
	b, err := arc.NewBuilder(arc.Options{Namespace: "Tasks", Routes: &routes})
	if err != nil {
		t.Fatal(err)
	}
	routes.RoutePrefix = "mutated"
	if err := commands.Register[builderCommand](b); err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[builderModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) ([]builderModel, error) { return []builderModel{}, nil })); err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if a.Catalog().Commands[0].Type.Namespace != "Tasks" || a.Catalog().Queries[0].ReadModel.Namespace != "Tasks" {
		t.Fatal(a.Catalog())
	}
	if a.Endpoints()[0].Path == "/mutated/builder-command" {
		t.Fatal("retained route options")
	}
	c := a.Catalog()
	c.Commands[0].Type.Name = "changed"
	if a.Catalog().Commands[0].Type.Name == "changed" {
		t.Fatal("catalog alias")
	}
	if err := commands.Register[builderCommand](b); !errors.Is(err, arc.ErrFrozen) {
		t.Fatal(err)
	}
	again, err := b.Build()
	if err != nil || again != a {
		t.Fatal(again, err)
	}
}
func TestBuilderNamespaceHintIsNotAnExportedMethod(t *testing.T) {
	if _, exists := reflect.TypeFor[*arc.Builder]().MethodByName("CommandNamespace"); exists {
		t.Fatal("namespace implementation hint escaped into public Builder API")
	}
	for _, root := range []bool{false, true} {
		b, err := arc.NewBuilder(arc.Options{Namespace: "Tasks"})
		if err != nil {
			t.Fatal(err)
		}
		var registrar commands.Registrar = b.Commands()
		if root {
			registrar = b
		}
		if err := commands.Register[builderCommand](registrar); err != nil {
			t.Fatal(err)
		}
		if got := b.Catalog().Commands[0].Type.Namespace; got != "Tasks" {
			t.Fatal(root, got)
		}
	}
}

func TestBuilderFailedAttemptIsTerminal(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[builderCommand](b, commands.WithPath[builderCommand]("/.cratis/me")); err != nil {
		t.Fatal(err)
	}
	// Freeze a borrowed child incorrectly: root must diagnose and cache failure.
	if _, err := b.Commands().Build(commands.PipelineOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = b.Build()
	if err == nil {
		t.Fatal("expected composition error")
	}
	_, second := b.Build()
	if second != err {
		t.Fatal("failed Build was retried")
	}
}
