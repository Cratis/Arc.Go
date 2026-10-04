// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"fmt"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/queries"
)

func ExampleQueryHealth() {
	builder, err := arc.NewBuilder(arc.Options{
		QueryHealth: &arc.QueryHealthOptions{Roles: []string{"Operations"}},
	})
	if err != nil {
		panic(err)
	}
	application, err := builder.Build()
	if err != nil {
		panic(err)
	}
	if err := application.Start(context.Background()); err != nil {
		panic(err)
	}
	// Backend code supplies a trusted identity. HTTP callers instead require an
	// Authentication handler that verifies credentials and supplies this role.
	ctx := identity.WithPrincipal(context.Background(), identity.System("Operations"))
	result, err := queries.Perform[arc.QueryHealth](ctx, application.Queries(), arc.QueryHealthName, queries.Request{})
	if err != nil {
		panic(err)
	}
	health, present := result.Data()
	fmt.Println(result.IsSuccess(), present, len(health.Observations))
	if err := application.Shutdown(context.Background()); err != nil {
		panic(err)
	}
	// Output: true true 0
}
