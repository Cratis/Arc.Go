// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Command nocontainer demonstrates Arc's foundation flows with plain Go wiring.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

type greeting struct {
	Name string `json:"name" validate:"required"`
}

type greeter struct{}

func (greeter) greet(name string) string { return "hello, " + name }

type resources struct {
	greeter greeter
	closed  bool
}

func (r *resources) Close(context.Context) error { r.closed = true; return nil }

var errInvalidName = errors.New("a name is required")

func run(ctx context.Context, output io.Writer, principal identity.Principal, name string) error {
	catalog := metadata.Catalog{Version: metadata.Version, Commands: []metadata.Command{{
		Type: metadata.TypeName{Name: "Greet"},
		Authorization: &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{
			Roles: []string{"Greeter"},
		}}},
	}}}
	var policies authorization.Registry
	evaluator, err := policies.Build(catalog, authorization.Options{})
	if err != nil {
		return err
	}
	var graph validation.Graph
	holder := &resources{greeter: greeter{}}
	open := func(context.Context) (execution.Resources, error) { return holder, nil }
	err = execution.RunWithResources(ctx, open, execution.Metadata{Principal: principal}, 0,
		func(ctx context.Context, scope *execution.Scope) error {
			prepared, err := evaluator.Prepare(ctx, authorization.Target{Kind: authorization.Command, Identity: "Greet"})
			if err != nil {
				return err
			}
			decision, err := prepared.EvaluateScoped(ctx, scope, nil)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(output, "authorized:", decision.IsAllowed()); err != nil {
				return err
			}
			if !decision.IsAllowed() {
				return decision.Err()
			}
			findings, err := graph.Validate(ctx, scope, greeting{Name: name})
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(output, "validation findings:", len(findings)); err != nil {
				return err
			}
			if len(findings) != 0 {
				return errInvalidName
			}
			wired, err := execution.ResourcesAs[*resources](ctx, scope)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(output, wired.greeter.greet(name))
			return err
		})
	_, outputErr := fmt.Fprintln(output, "resources closed:", holder.closed)
	return errors.Join(err, outputErr)
}

func main() {
	// A trusted system actor still needs the declared role; it has no bypass.
	if err := run(context.Background(), os.Stdout, identity.System("Greeter"), "Arc"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
