// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

type dependencyCatalog struct{ key di.Key }

func (c *dependencyCatalog) Contains(key di.Key) bool { return c.key == key }

func TestGraphDependencyManifestsAreCopiedAndCheckedWithoutActivation(t *testing.T) {
	for _, concept := range []bool{false, true} {
		var registry validation.Registry
		keys := []di.Key{di.KeyFor[int]()}
		factory := func(context.Context, *execution.Scope) (validation.Validator[string], error) {
			t.Fatal("dependency check activated validator")
			return nil, nil
		}
		register := validation.RegisterScoped[string]
		if concept {
			register = validation.RegisterScopedConcept[string]
		}
		if err := register(&registry, factory, di.Key{}); !errors.Is(err, validation.ErrInvalidRegistration) {
			t.Fatal(err)
		}
		if err := register(&registry, factory, keys...); err != nil {
			t.Fatal(err)
		}
		keys[0] = di.KeyFor[bool]()
		graph, err := registry.Build()
		if err != nil {
			t.Fatal(err)
		}
		var typedNil *dependencyCatalog
		for _, catalog := range []di.Catalog{nil, typedNil, &dependencyCatalog{key: di.KeyFor[bool]()}} {
			if !errors.Is(graph.CheckDependencies(catalog), validation.ErrInvalidRegistration) {
				t.Fatal("missing dependency accepted")
			}
		}
		if err := graph.CheckDependencies(&dependencyCatalog{key: di.KeyFor[int]()}); err != nil {
			t.Fatal(err)
		}
	}
	var empty *validation.Graph
	if err := empty.CheckDependencies(nil); err != nil {
		t.Fatal(err)
	}
}
