// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cratis/arc.go/concepts"
)

func TestGoldenScalars(t *testing.T) {
	data, err := os.ReadFile("fixtures/v1/scalars.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]json.RawMessage
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	cases := map[string]any{
		"uuid": new(concepts.UUID), "emptyUUID": new(concepts.UUID),
		"date": new(concepts.DateOnly), "minimumDate": new(concepts.DateOnly), "maximumDate": new(concepts.DateOnly),
		"time": new(concepts.TimeOnly), "midnight": new(concepts.TimeOnly),
		"duration": new(concepts.TimeSpan), "zeroDuration": new(concepts.TimeSpan), "minimumDuration": new(concepts.TimeSpan), "maximumDuration": new(concepts.TimeSpan),
	}
	if len(fixtures) != 11 || len(cases) != len(fixtures) {
		t.Fatal("scalar fixture coverage changed")
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			target, ok := cases[name]
			if !ok {
				t.Fatal("missing scalar case")
			}
			if err := json.Unmarshal(fixture, target); err != nil {
				t.Fatal(err)
			}
			actual, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(actual) != string(fixture) {
				t.Fatalf("JSON = %s, want %s", actual, fixture)
			}
		})
	}
}
