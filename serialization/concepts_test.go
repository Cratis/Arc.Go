// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"reflect"
	"testing"

	"github.com/cratis/arc.go/concepts"
	fconcepts "github.com/cratis/fundamentals.go/concepts"
)

func TestSharedScalarPlanMetadata(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		kind fconcepts.ScalarKind
	}{
		{reflect.TypeFor[concepts.UUID](), fconcepts.KindUUID},
		{reflect.TypeFor[concepts.DateOnly](), fconcepts.KindDateOnly},
		{reflect.TypeFor[concepts.TimeOnly](), fconcepts.KindTimeOnly},
		{reflect.TypeFor[concepts.TimeSpan](), fconcepts.KindTimeSpan},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			r, ok, err := conceptType(reflect.PointerTo(tc.typ))
			if err != nil || !ok || r.Type != tc.typ || r.Declared != tc.typ || r.Kind != tc.kind || r.PointerDepth != 1 {
				t.Fatalf("representation = %+v, %v, %v", r, ok, err)
			}
		})
	}
}

func TestOrdinaryTypesAreNotConcepts(t *testing.T) {
	type ordinary string
	_, ok, err := conceptType(reflect.TypeFor[ordinary]())
	if err != nil || ok {
		t.Fatalf("recognition = %v, %v", ok, err)
	}
}
