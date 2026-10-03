// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/metadata"
)

type manualCommand struct{}

func contractBuilder(t *testing.T, routes metadata.Options) *arc.Builder {
	t.Helper()
	builder, err := arc.NewBuilder(arc.Options{Namespace: "Shop.Tasks", Routes: &routes})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[generatedCommand](builder, commands.WithName[generatedCommand]("Register"),
		commands.Void(func(generatedCommand, context.Context) error { t.Fatal("Build activated handler"); return nil })); err != nil {
		t.Fatal(err)
	}
	return builder
}

func TestGeneratedEndpointContractAgreesAndCopiesInputs(t *testing.T) {
	builder := contractBuilder(t, metadata.DefaultOptions())
	expected, err := metadata.Resolve(builder.Catalog(), metadata.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.ExpectGeneratedEndpoints("sha256:fixture", expected); err != nil {
		t.Fatal(err)
	}
	expected[0].Path = "/mutated"
	if _, err := builder.Build(); err != nil {
		t.Fatal(err)
	}
	if err := builder.ExpectGeneratedEndpoints("frozen", expected); !errors.Is(err, arc.ErrFrozen) {
		t.Fatal(err)
	}
}

func TestGeneratedEndpointContractRejectsRouteDriftBeforeBuild(t *testing.T) {
	for _, change := range []string{"prefix", "namespace", "manual-collision", "missing-validation", "missing-artifact"} {
		t.Run(change, func(t *testing.T) {
			options := metadata.DefaultOptions()
			options.IncludeCommandName = false
			builder := contractBuilder(t, options)
			expected, err := metadata.Resolve(builder.Catalog(), options)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "prefix":
				expected[0].Path = "/different"
			case "namespace":
				for i := range expected {
					expected[i].Identity = "Elsewhere.Register"
				}
			case "manual-collision":
				if err := commands.Register[manualCommand](builder, commands.WithDescriptor[manualCommand](metadata.Command{Type: metadata.TypeName{Namespace: "Shop.Tasks", Name: "Manual"}}), commands.Void(func(manualCommand, context.Context) error { return nil })); err != nil {
					t.Fatal(err)
				}
			case "missing-validation":
				expected = slices.DeleteFunc(expected, func(e metadata.Endpoint) bool { return e.ValidateOnly })
			case "missing-artifact":
				for i := range expected {
					expected[i].Identity = "Shop.Tasks.Absent"
				}
			}
			if err := builder.ExpectGeneratedEndpoints("profile", expected); err != nil {
				t.Fatal(err)
			}
			_, err = builder.Build()
			var mismatch *metadata.GeneratedContractMismatchError
			if !errors.As(err, &mismatch) || !strings.Contains(err.Error(), "expected endpoints") || !strings.Contains(err.Error(), "actual endpoints") {
				t.Fatal("missing inspectable drift diagnostic", err)
			}
		})
	}
}

func TestGeneratedEndpointContractRejectsOverlappingOwnership(t *testing.T) {
	builder := contractBuilder(t, metadata.DefaultOptions())
	expected, err := metadata.Resolve(builder.Catalog(), metadata.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.ExpectGeneratedEndpoints("one", expected); err != nil {
		t.Fatal(err)
	}
	if err := builder.ExpectGeneratedEndpoints("two", expected); err == nil {
		t.Fatal("overlapping ownership accepted")
	}
	if err := builder.ExpectGeneratedEndpoints("", expected); err == nil {
		t.Fatal("empty profile accepted")
	}
	if err := builder.ExpectGeneratedEndpoints("empty", nil); err == nil {
		t.Fatal("empty ownership accepted")
	}
}
