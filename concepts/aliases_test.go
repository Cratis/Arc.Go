// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts_test

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/serialization"
	fconcepts "github.com/cratis/fundamentals.go/concepts"
)

func TestSharedScalarTypeIdentity(t *testing.T) {
	for _, pair := range [][2]reflect.Type{
		{reflect.TypeFor[concepts.UUID](), reflect.TypeFor[fconcepts.UUID]()},
		{reflect.TypeFor[concepts.DateOnly](), reflect.TypeFor[fconcepts.DateOnly]()},
		{reflect.TypeFor[concepts.TimeOnly](), reflect.TypeFor[fconcepts.TimeOnly]()},
		{reflect.TypeFor[concepts.TimeSpan](), reflect.TypeFor[fconcepts.TimeSpan]()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("Arc type %v differs from shared type %v", pair[0], pair[1])
		}
	}
}

func TestSharedScalarRoundTrips(t *testing.T) {
	t.Run("UUID", func(t *testing.T) {
		arcValue, err := concepts.ParseUUID("00112233-4455-4677-8899-AABBCCDDEEFF")
		if err != nil {
			t.Fatal(err)
		}
		sharedValue, err := fconcepts.ParseUUID("00112233-4455-4677-8899-aabbccddeeff")
		if err != nil {
			t.Fatal(err)
		}
		assertSharedRoundTrip(t, arcValue, sharedValue)
	})
	t.Run("DateOnly", func(t *testing.T) {
		arcValue, err := concepts.NewDateOnly(2024, time.February, 29)
		if err != nil {
			t.Fatal(err)
		}
		sharedValue, err := fconcepts.NewDateOnly(2024, time.February, 29)
		if err != nil {
			t.Fatal(err)
		}
		assertSharedRoundTrip(t, arcValue, sharedValue)
	})
	t.Run("TimeOnly", func(t *testing.T) {
		arcValue, err := concepts.NewTimeOnly(37_234_000_000)
		if err != nil {
			t.Fatal(err)
		}
		sharedValue, err := fconcepts.NewTimeOnly(37_234_000_000)
		if err != nil {
			t.Fatal(err)
		}
		assertSharedRoundTrip(t, arcValue, sharedValue)
	})
	t.Run("TimeSpan", func(t *testing.T) {
		arcValue, err := concepts.ParseTimeSpan("-10675199.02:48:05.4775808")
		if err != nil {
			t.Fatal(err)
		}
		assertSharedRoundTrip(t, arcValue, fconcepts.TimeSpan(math.MinInt64))
	})
}

// A single inferred T also proves both packages' values are directly assignable.
func assertSharedRoundTrip[T comparable](t *testing.T, arcValue, sharedValue T) {
	t.Helper()
	if arcValue != sharedValue {
		t.Fatalf("Arc value = %v, shared value = %v", arcValue, sharedValue)
	}
	arcJSON, err := serialization.Marshal(arcValue)
	if err != nil {
		t.Fatal(err)
	}
	sharedJSON, err := json.Marshal(sharedValue)
	if err != nil {
		t.Fatal(err)
	}
	if string(arcJSON) != string(sharedJSON) {
		t.Fatalf("Arc JSON = %s, shared JSON = %s", arcJSON, sharedJSON)
	}
	var fromArc, fromShared T
	if err := json.Unmarshal(arcJSON, &fromArc); err != nil {
		t.Fatal(err)
	}
	if err := serialization.Unmarshal(sharedJSON, &fromShared); err != nil {
		t.Fatal(err)
	}
	if fromArc != sharedValue || fromShared != arcValue {
		t.Fatalf("round trip = %v / %v, want %v", fromArc, fromShared, arcValue)
	}
}
