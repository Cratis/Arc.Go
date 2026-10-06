// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authorization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

type dependencyCatalog struct{ key di.Key }

func (c *dependencyCatalog) Contains(key di.Key) bool { return c.key == key }

func TestPolicyDependencyManifestsCheckOnlyEffectiveCatalogSubset(t *testing.T) {
	var registry authorization.Registry
	factory := func(context.Context, *execution.Scope) (authorization.PolicyFunc, error) {
		t.Fatal("dependency check constructed policy")
		return nil, nil
	}
	if err := authorization.RegisterPolicy(&registry, "p", factory, authorization.PolicyOptions{}, di.Key{}); !errors.Is(err, authorization.ErrInvalidConfiguration) {
		t.Fatal(err)
	}
	keys := []di.Key{di.KeyFor[int]()}
	if err := authorization.RegisterPolicy(&registry, "p", factory, authorization.PolicyOptions{}, keys...); err != nil {
		t.Fatal(err)
	}
	keys[0] = di.KeyFor[bool]()
	c := catalog(&metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "p"}}})
	c.Queries = []metadata.Query{{ReadModel: metadata.TypeName{Name: "Row"}, Name: "All", ReadModelAuthorization: c.Commands[0].Authorization, Authorization: &metadata.Authorization{AllowAnonymous: true}}}
	e := build(t, &registry, c, authorization.Options{})
	var typedNil *dependencyCatalog
	for _, dependencies := range []di.Catalog{nil, typedNil, &dependencyCatalog{key: di.KeyFor[bool]()}} {
		if !errors.Is(e.CheckDependencies(c, dependencies), authorization.ErrInvalidConfiguration) {
			t.Fatal("missing policy dependency accepted")
		}
	}
	if err := e.CheckDependencies(c, &dependencyCatalog{key: di.KeyFor[int]()}); err != nil {
		t.Fatal(err)
	}
	if err := e.CheckDependencies(metadata.Catalog{Version: metadata.Version, Queries: c.Queries}, nil); err != nil {
		t.Fatal("overridden policy required an unused dependency", err)
	}
}
