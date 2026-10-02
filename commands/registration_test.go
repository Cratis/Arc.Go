// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/metadata"
)

type Rename struct {
	Name string `json:"name"`
}

func (c Rename) Handle(context.Context) (string, error) { return c.Name, nil }

type Clear struct{}

func (Clear) Handle(context.Context) error { return nil }

type PointerCommand struct{}

func (*PointerCommand) Handle(context.Context) error { return nil }
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func build(t *testing.T, r *commands.Registry, options commands.PipelineOptions) commands.Pipeline {
	t.Helper()
	p, err := r.Build(options)
	must(t, err)
	return p
}
func TestComposableRegistration(t *testing.T) {
	r, err := commands.NewRegistry(commands.RegistryOptions{Namespace: "Shop"})
	must(t, err)
	must(t, commands.Register[Clear](r))
	// Adapter-driven inference is part of the public contract.
	must(t, commands.Register(r, commands.Handle(Rename.Handle)))
	must(t, commands.Register[*PointerCommand](r))
	if err := commands.Register[PointerCommand](r); !errors.Is(err, commands.ErrMissingHandler) {
		t.Fatalf("value synthesized pointer method: %v", err)
	}
	p := build(t, r, commands.PipelineOptions{})
	registration, ok := p.Lookup("Shop.Rename")
	if !ok {
		t.Fatal("missing identity")
	}
	if registration.CommandType() != reflect.TypeFor[Rename]() || registration.ReturnType() != reflect.TypeFor[string]() {
		t.Fatal("incorrect type metadata")
	}
	if typ, known := registration.ResponseType(); !known || typ != reflect.TypeFor[string]() {
		t.Fatal("incorrect response metadata")
	}
	result, err := commands.Execute[string](t.Context(), p, Rename{"Ada"})
	must(t, err)
	if response, present := result.Response(); !result.IsSuccess() || !present || response != "Ada" {
		t.Fatalf("result: %+v", result)
	}
	if _, err := p.LookupCommand((*PointerCommand)(nil)); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if err := commands.Register[Clear](r); !errors.Is(err, commands.ErrFrozen) {
		t.Fatal(err)
	}
}
func TestRegistrationValidationAndSnapshots(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r))
	if err := commands.Register[Clear](&r); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
	if err := commands.Register[struct{}](&r); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	if err := commands.Register[Rename](&r, commands.Handle(Rename.Handle), commands.Handle(Rename.Handle)); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
	if err := commands.Register[Rename](&r, commands.Handle(Rename.Handle), commands.WithPath[Rename]("/a"), commands.WithPath[Rename]("/b")); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
	auth := metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Roles: []string{"Editor"}}}}
	must(t, commands.Register[Rename](&r, commands.Handle(Rename.Handle), commands.WithNamespace[Rename]("Overridden"), commands.WithDescriptor[Rename](metadata.Command{Type: metadata.TypeName{Namespace: "Base", Name: "Pinned"}, Authorization: &auth})))
	auth.Requirements[0].Roles[0] = "Changed"
	catalog := r.Catalog()
	catalog.Commands[1].Authorization.Requirements[0].Roles[0] = "Leaked"
	if got := r.Catalog().Commands[1]; got.Type.Identity() != "Overridden.Pinned" || got.Authorization.Requirements[0].Roles[0] != "Editor" {
		t.Fatalf("snapshot leaked: %+v", got)
	}
	if _, err := r.Build(commands.PipelineOptions{CleanupTimeout: -1}); err == nil {
		t.Fatal("invalid build passed")
	}
	must(t, commands.Register[*PointerCommand](&r))
	p := build(t, &r, commands.PipelineOptions{})
	if _, err := commands.Execute[int](t.Context(), p, Rename{}); !errors.Is(err, commands.ErrResponseType) {
		t.Fatal(err)
	}
}
func TestPointerAndValueRegistrationsAreExact(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Rename](&r, commands.Handle(Rename.Handle)))
	must(t, commands.Register[*Rename](&r, commands.Handle(func(c *Rename, ctx context.Context) (string, error) { return c.Handle(ctx) }), commands.WithDescriptor[*Rename](metadata.Command{Type: metadata.TypeName{Name: "PointerRename"}})))
	p := build(t, &r, commands.PipelineOptions{})
	for _, value := range []any{Rename{"value"}, &Rename{"pointer"}} {
		result, err := p.Execute(t.Context(), value)
		must(t, err)
		if !result.IsSuccess() {
			t.Fatal(result.Details())
		}
	}
}
