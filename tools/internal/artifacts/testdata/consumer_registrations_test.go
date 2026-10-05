// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

type allowList map[string]bool

func (a allowList) Allowed(id string) bool { return a[id] }

type counter struct{ evaluations atomic.Int32 }

func (c *counter) Evaluated() { c.evaluations.Add(1) }

type outcome struct {
	Authorized bool
	Validation []validation.Result
	Exceptions []string
}

type scenario struct {
	name    string
	ctx     func(context.Context) context.Context
	command any
}

var (
	editor = func(ctx context.Context) context.Context {
		return identity.WithPrincipal(ctx, identity.NewPrincipal(identity.PrincipalData{ID: "e", AuthenticationType: "test", Roles: []string{"Editor"}}))
	}
	reader = func(ctx context.Context) context.Context {
		return identity.WithPrincipal(ctx, identity.NewPrincipal(identity.PrincipalData{ID: "r", AuthenticationType: "test"}))
	}
	guest     = func(ctx context.Context) context.Context { return ctx }
	scenarios = []scenario{
		{"valid rename by editor", editor, rename{Name: "n", Code: "UP", Owner: owner{ID: "known"}}},
		{"missing name", editor, rename{Code: "UP", Owner: owner{ID: "known"}}},
		{"lower-case concept", editor, rename{Name: "n", Code: "low", Owner: owner{ID: "known"}}},
		{"unknown nested owner", editor, rename{Name: "n", Code: "UP", Owner: owner{ID: "stranger"}}},
		{"rename by non-editor", reader, rename{Name: "n", Code: "UP", Owner: owner{ID: "known"}}},
		{"rename by guest", guest, rename{Name: "n", Code: "UP", Owner: owner{ID: "known"}}},
		{"audit by guest", guest, audit{Note: "x"}},
		{"audit by reader", reader, audit{Note: "x"}},
	}
)

// manual is the registration an application would write by hand with the
// public registrars, for exactly the declarations the generator owns.
func manual(t *testing.T, builder *arc.Builder, rules ownerRules, log auditLog) {
	t.Helper()
	noop := func(context.Context, *commands.Invocation, rename) (commands.NoResponse, error) {
		return commands.NoResponse{}, nil
	}
	if err := commands.Register[rename](builder, commands.Invoke(noop), commands.WithName[rename]("Rename"), commands.WithPath[rename](""),
		commands.WithAuthorization[rename](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "Editors"}}})); err != nil {
		t.Fatal(err)
	}
	auditNoop := func(context.Context, *commands.Invocation, audit) (commands.NoResponse, error) {
		return commands.NoResponse{}, nil
	}
	if err := commands.Register[audit](builder, commands.Invoke(auditNoop), commands.WithName[audit]("Audit"), commands.WithPath[audit](""),
		commands.WithAuthorization[audit](metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "Auditors"}}})); err != nil {
		t.Fatal(err)
	}
	if err := validation.Register[rename](builder.Validators(), renameValidator{}); err != nil {
		t.Fatal(err)
	}
	if err := validation.RegisterConcept[code](builder.Validators(), &codeValidator{}); err != nil {
		t.Fatal(err)
	}
	if err := validation.RegisterScoped[owner](builder.Validators(), func(ctx context.Context, _ *execution.Scope) (validation.Validator[owner], error) {
		return newOwnerValidator(ctx, rules)
	}); err != nil {
		t.Fatal(err)
	}
	if err := builder.Policies().Register("Editors", editors{}, authorization.PolicyOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := authorization.RegisterPolicy(builder.Policies(), "Auditors", func(context.Context, *execution.Scope) (auditors, error) {
		return newAuditors(log), nil
	}, authorization.PolicyOptions{EvaluatesAnonymous: true}); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, options arc.Options, register func(*arc.Builder) error) ([]outcome, *arc.Application) {
	t.Helper()
	builder, err := arc.NewBuilder(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := register(builder); err != nil {
		t.Fatal(err)
	}
	app, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	outcomes := make([]outcome, 0, len(scenarios))
	for _, s := range scenarios {
		result, err := app.Commands().Execute(s.ctx(t.Context()), s.command)
		if err != nil {
			t.Fatal(s.name, err)
		}
		details := result.Details()
		outcomes = append(outcomes, outcome{details.Authorized, details.ValidationResults, details.ExceptionMessages})
	}
	return outcomes, app
}

func TestGeneratedRegistrationsMatchManualRegistration(t *testing.T) {
	rules := allowList{"known": true}
	var manualLog, generatedLog, containerLog counter
	want, _ := run(t, arc.Options{}, func(b *arc.Builder) error { manual(t, b, rules, &manualLog); return nil })

	var resolved atomic.Int32
	generated, _ := run(t, arc.Options{}, func(b *arc.Builder) error {
		return RegisterArtifacts(b, ArcBindings{
			ResolveOwnerRules: func(context.Context, *execution.Scope) (ownerRules, error) { resolved.Add(1); return rules, nil },
			ResolveAuditLog:   func(context.Context, *execution.Scope) (auditLog, error) { return &generatedLog, nil },
		})
	})
	if !reflect.DeepEqual(want, generated) {
		t.Fatalf("generated outcomes differ from manual registration\nmanual:    %+v\ngenerated: %+v", want, generated)
	}
	if manualLog.evaluations.Load() != generatedLog.evaluations.Load() || generatedLog.evaluations.Load() != 2 {
		t.Fatalf("scoped policy evaluations manual=%d generated=%d, want 2", manualLog.evaluations.Load(), generatedLog.evaluations.Load())
	}
	if resolved.Load() == 0 {
		t.Fatal("scoped validator dependency was never resolved")
	}

	var registry container.Registry
	if err := di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (ownerRules, error) { return rules, nil }); err != nil {
		t.Fatal(err)
	}
	if err := di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (auditLog, error) { return &containerLog, nil }); err != nil {
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
	fromContainer, _ := run(t, arc.Options{ScopeFactory: provider}, func(b *arc.Builder) error { return RegisterArtifacts(b) })
	if !reflect.DeepEqual(want, fromContainer) {
		t.Fatalf("container-resolved outcomes differ from manual registration\nmanual:    %+v\ncontainer: %+v", want, fromContainer)
	}
	expectations := []struct {
		authorized bool
		messages   []string
	}{{true, nil}, {true, []string{"Name required"}}, {true, []string{"Code must be upper case"}}, {true, []string{"Owner not allowed"}}, {false, nil}, {false, nil}, {true, nil}, {false, nil}}
	for i, e := range expectations {
		got := want[i]
		var messages []string
		for _, r := range got.Validation {
			messages = append(messages, r.Message)
		}
		if got.Authorized != e.authorized || !reflect.DeepEqual(messages, e.messages) {
			t.Fatalf("%s: authorized=%t messages=%v, want %t %v", scenarios[i].name, got.Authorized, messages, e.authorized, e.messages)
		}
	}
}

func TestGeneratedRegistrationsOccupyTheManualIdentities(t *testing.T) {
	builder, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterArtifacts(builder); err != nil {
		t.Fatal(err)
	}
	duplicates := map[string]error{
		"rename validator": validation.Register[rename](builder.Validators(), renameValidator{}),
		"code concept":     validation.RegisterConcept[code](builder.Validators(), &codeValidator{}),
		"owner scoped": validation.RegisterScoped[owner](builder.Validators(), func(context.Context, *execution.Scope) (validation.Validator[owner], error) {
			return nil, errors.New("unused")
		}),
	}
	for name, err := range duplicates {
		if !errors.Is(err, validation.ErrDuplicate) {
			t.Fatalf("%s: err = %v, want validation.ErrDuplicate", name, err)
		}
	}
	for _, name := range []string{"Editors", "Auditors"} {
		if err := builder.Policies().Register(name, editors{}, authorization.PolicyOptions{}); !errors.Is(err, authorization.ErrDuplicate) {
			t.Fatalf("policy %s: err = %v, want authorization.ErrDuplicate", name, err)
		}
	}
}

func TestGeneratedScopedRegistrationsDeclareDependencyKeys(t *testing.T) {
	rules := func(context.Context, *execution.Scope) (ownerRules, error) { return allowList{"known": true}, nil }
	log := func(context.Context, *execution.Scope) (auditLog, error) { return &counter{}, nil }
	for _, tc := range []struct {
		name     string
		bindings ArcBindings
		want     error
	}{
		{"missing ownerRules", ArcBindings{ResolveAuditLog: log}, validation.ErrInvalidRegistration},
		{"missing auditLog", ArcBindings{ResolveOwnerRules: rules}, authorization.ErrInvalidConfiguration},
		{"both keys supplied", ArcBindings{ResolveOwnerRules: rules, ResolveAuditLog: log}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder, err := arc.NewBuilder(arc.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := RegisterArtifacts(builder, tc.bindings); err != nil {
				t.Fatal(err)
			}
			if _, err := builder.Build(); !errors.Is(err, tc.want) {
				t.Fatalf("Build error = %v, want %v", err, tc.want)
			}
		})
	}
}
