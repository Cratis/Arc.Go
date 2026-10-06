// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"

	"example.test/consumer/access"
	"github.com/cratis/arc.go/serialization"
)

type enumFixture struct {
	Reads []struct {
		Type, ID, Input    string
		Nullable, Accepted bool
		Value              *string
		Write              *struct {
			Accepted bool
			Output   *string
		}
	}
	Writes []struct {
		Type, ID, Value string
		Accepted        bool
		Output          *string
	}
}

func TestGeneratedParsersConsumePinnedInt32Corpus(t *testing.T) {
	data, err := os.ReadFile("profile.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture enumFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	t.Run("State", func(t *testing.T) { checkEnumCorpus[State](t, fixture, "State", 53, 5) })
	t.Run("Access", func(t *testing.T) { checkEnumCorpus[access.Access](t, fixture, "Access", 12, 3) })
	var value State
	if err := serialization.Unmarshal([]byte(`"Reader"`), &value); !errors.Is(err, serialization.ErrInvalidEnumValue) {
		t.Fatalf("TS rename became parse name: %v", err)
	}
}

func checkEnumCorpus[T ~int32](t *testing.T, fixture enumFixture, name string, wantReads, wantWrites int) {
	t.Helper()
	reads, writes := 0, 0
	for _, row := range fixture.Reads {
		if row.Type != name {
			continue
		}
		reads++
		t.Run("read/"+row.ID, func(t *testing.T) {
			if row.Nullable && row.Input == "null" {
				var value *T
				if err := serialization.Unmarshal([]byte(row.Input), &value); err != nil || value != nil || !row.Accepted {
					t.Fatalf("nullable=%v error=%v", value, err)
				}
				return
			}
			value := T(17)
			parser := any(&value).(interface{ UnmarshalJSON([]byte) error })
			err := parser.UnmarshalJSON([]byte(row.Input))
			if (err == nil) != row.Accepted {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, row.Accepted, err)
			}
			bound := T(17)
			bindErr := serialization.Unmarshal([]byte(row.Input), &bound)
			if err != nil {
				if !errors.Is(err, serialization.ErrInvalidEnumValue) || bindErr == nil || value != 17 || bound != 17 {
					t.Fatalf("failure changed receiver=%d bound=%d errors=%v / %v", value, bound, err, bindErr)
				}
				return
			}
			if row.Value == nil || strconv.FormatInt(int64(value), 10) != *row.Value {
				t.Fatalf("value=%d want=%v", value, row.Value)
			}
			if bindErr != nil || bound != value {
				t.Fatalf("bound=%d want=%d error=%v", bound, value, bindErr)
			}
			wire, err := serialization.Marshal(value)
			if err != nil || row.Write == nil || !row.Write.Accepted || row.Write.Output == nil || string(wire) != *row.Write.Output {
				t.Fatalf("wire=%s error=%v", wire, err)
			}
		})
	}
	for _, row := range fixture.Writes {
		if row.Type != name {
			continue
		}
		writes++
		t.Run("write/"+row.ID, func(t *testing.T) {
			value, err := strconv.ParseInt(row.Value, 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := serialization.Marshal(T(value))
			if err != nil || !row.Accepted || row.Output == nil || string(wire) != *row.Output {
				t.Fatalf("wire=%s want=%v error=%v", wire, row.Output, err)
			}
		})
	}
	if reads != wantReads || writes != wantWrites {
		t.Fatalf("incomplete %s corpus: reads=%d want=%d writes=%d want=%d", name, reads, wantReads, writes, wantWrites)
	}
}
