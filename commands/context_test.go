// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type valueProvider func(context.Context, *commands.Invocation) (commands.ContextValues, error)

func (f valueProvider) Provide(ctx context.Context, inv *commands.Invocation) (commands.ContextValues, error) {
	return f(ctx, inv)
}

type keyResolver func(context.Context, *commands.Invocation) (string, bool, error)

func (f keyResolver) Resolve(ctx context.Context, inv *commands.Invocation) (string, bool, error) {
	return f(ctx, inv)
}

type Keyed struct {
	ID string `json:"id" arc:"key"`
}

func (c Keyed) GetKey() string             { return "provider" }
func (Keyed) Handle(context.Context) error { return nil }
func TestContextValuesCopiedAndCaseInsensitive(t *testing.T) {
	input := map[string]any{"Source": "api"}
	values, err := commands.NewContextValues(input)
	must(t, err)
	input["Source"] = "changed"
	entries := values.Entries()
	entries["source"] = "leaked"
	if got, ok := values.Get("SOURCE"); !ok || got != "api" {
		t.Fatal(got)
	}
	if _, err := commands.NewContextValues(map[string]any{"Source": 1, "source": 2}); !errors.Is(err, commands.ErrDuplicate) {
		t.Fatal(err)
	}
}
func TestKeyPrecedenceAndEmptyAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, custom  string
		authoritative bool
		want          string
	}{
		{"empty authority", "custom", true, ""}, {"custom", "custom", false, "custom"}, {"command provider", "", false, "provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r commands.Registry
			must(t, commands.Register[Keyed](&r, commands.WithKey[Keyed](func(Keyed) (string, bool) { return "selector", true })))
			if tc.authoritative {
				must(t, r.AddContextValuesProvider("key", func(context.Context, *execution.Scope) (commands.ContextValuesProvider, error) {
					return valueProvider(func(context.Context, *commands.Invocation) (commands.ContextValues, error) {
						return commands.NewContextValues(map[string]any{"ResolvedKey": ""})
					}), nil
				}))
			}
			must(t, r.AddKeyResolver("custom", func(context.Context, *execution.Scope) (commands.KeyResolver, error) {
				return keyResolver(func(context.Context, *commands.Invocation) (string, bool, error) {
					if tc.authoritative {
						t.Fatal("fallback after authoritative key")
					}
					return tc.custom, true, nil
				}), nil
			}))
			must(t, r.AddFilter("observe", func(context.Context, *execution.Scope) (commands.Filter, error) {
				return commands.FilterFunc(func(ctx context.Context, inv *commands.Invocation) (commands.Result[commands.NoResponse], error) {
					key, present := inv.CommandContext().ResolvedKey()
					if !present || key != tc.want {
						t.Fatalf("key = %q, %v", key, present)
					}
					ambient, ok := commands.ContextFrom(ctx)
					if !ok || ambient.Descriptor().Type.Name != "Keyed" {
						t.Fatal("missing callback metadata")
					}
					return commands.Success(inv.CommandContext().CorrelationID()), nil
				}), nil
			}))
			p := build(t, &r, commands.PipelineOptions{})
			result, err := p.Execute(t.Context(), Keyed{"tag"})
			must(t, err)
			if !result.IsSuccess() {
				t.Fatal(result.Details())
			}
		})
	}
}

type Tagged struct {
	ID string `json:"id" arc:"key"`
}

func (Tagged) Handle(context.Context) error { return nil }

type Unkeyed struct {
	ID string `json:"id"`
}

func (Unkeyed) Handle(context.Context) error { return nil }

type Ambiguous struct {
	A string `arc:"key"`
	B string `arc:"key"`
}

func (Ambiguous) Handle(context.Context) error { return nil }
func TestExplicitKeyTagAndNoIDInference(t *testing.T) {
	var r commands.Registry
	if err := commands.Register[Ambiguous](&r); !errors.Is(err, commands.ErrInvalidRegistration) {
		t.Fatal(err)
	}
	must(t, commands.Register[Tagged](&r))
	must(t, commands.Register[Unkeyed](&r, commands.WithValidator[Unkeyed](validation.ValidatorFunc[Unkeyed](func(ctx context.Context, _ Unkeyed) ([]validation.Result, error) {
		c, _ := commands.ContextFrom(ctx)
		if _, found := c.ResolvedKey(); found {
			t.Fatal("inferred undeclared key")
		}
		return nil, nil
	}))))
	p := build(t, &r, commands.PipelineOptions{})
	_, err := p.Execute(t.Context(), Unkeyed{ID: "not-a-key"})
	must(t, err)
}
