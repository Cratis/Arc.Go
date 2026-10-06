// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

func TestGeneratedParserConsumesPinnedStateCorpus(t *testing.T) {
	data, err := os.ReadFile("profile.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Reads []struct {
			Type, ID, Input    string
			Nullable, Accepted bool
			Value              *string
			Write              *struct {
				Accepted bool
				Output   *string
			}
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range fixture.Reads {
		if row.Type != "State" {
			continue
		}
		count++
		t.Run(row.ID, func(t *testing.T) {
			if row.Nullable && row.Input == "null" {
				var value *State
				if err := serialization.Unmarshal([]byte(row.Input), &value); err != nil || value != nil || !row.Accepted {
					t.Fatalf("nullable=%v error=%v", value, err)
				}
				return
			}
			value := State(17)
			err := value.UnmarshalJSON([]byte(row.Input))
			if (err == nil) != row.Accepted {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, row.Accepted, err)
			}
			if err != nil {
				if !errors.Is(err, serialization.ErrInvalidEnumValue) || value != 17 {
					t.Fatalf("failure changed receiver=%d error=%v", value, err)
				}
				return
			}
			if row.Value == nil || strconv.FormatInt(int64(value), 10) != *row.Value {
				t.Fatalf("value=%d want=%v", value, row.Value)
			}
			var bound State
			if err := serialization.Unmarshal([]byte(row.Input), &bound); err != nil || bound != value {
				t.Fatalf("bound=%d want=%d error=%v", bound, value, err)
			}
			wire, err := serialization.Marshal(value)
			if err != nil || !row.Write.Accepted || string(wire) != *row.Write.Output {
				t.Fatalf("wire=%s error=%v", wire, err)
			}
		})
	}
	if count == 0 {
		t.Fatal("missing State corpus")
	}
	var value State
	if err := serialization.Unmarshal([]byte(`"Reader"`), &value); !errors.Is(err, serialization.ErrInvalidEnumValue) {
		t.Fatalf("TS rename became parse name: %v", err)
	}
}
