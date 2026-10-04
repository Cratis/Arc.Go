// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package enumcontract_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

type outcome struct {
	Accepted bool
	Output   *string
	Error    *string
}

type readCase struct {
	ID, Type, Input string
	Nullable        bool
	Accepted        bool
	Value           *string
	Write           *outcome
	Error           *string
}

type writeCase struct {
	ID, Type, Value string
	Accepted        bool
	Output          *string
	Error           *string
}

type member struct{ Name, Value string }
type profile struct {
	Runtime, Arc, Fundamentals string
	Members                    map[string][]member
	Reads                      []readCase
	Writes                     []writeCase
	Inputs                     map[string]string
}

func loadJSON(t *testing.T, file string, target any) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func pinnedProfile(t *testing.T) profile {
	t.Helper()
	var actual profile
	loadJSON(t, "profile.json", &actual)
	if actual.Runtime != "10.0.12" || actual.Arc != "1.0.0+7c1e78075b737df64f69fddfaae83374f75e3612" || actual.Fundamentals != "1.0.0+14037b1ff8346b7944ad48065b8f66feca80dab5" {
		t.Fatal("unexpected reference profile")
	}
	files := []string{"corpus.json", "reference/Program.cs", "reference/Reference.csproj", "reference/packages.lock.json"}
	if len(actual.Inputs) != len(files) || len(actual.Reads) != 75 || len(actual.Writes) != 11 || len(actual.Members) != 5 {
		t.Fatal("incomplete profile inventory")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Inputs[file] != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("reference input drift: %s", file)
		}
	}
	var corpus struct {
		Reads  []readCase
		Writes []writeCase
	}
	loadJSON(t, "corpus.json", &corpus)
	if len(actual.Reads) != len(corpus.Reads) || len(actual.Writes) != len(corpus.Writes) {
		t.Fatal("missing observations")
	}
	seen := map[string]bool{}
	for index, row := range actual.Reads {
		input := corpus.Reads[index]
		key := row.Type + "/" + row.ID
		if seen[key] || row.ID != input.ID || row.Type != input.Type || row.Input != input.Input || row.Nullable != input.Nullable {
			t.Fatalf("changed or duplicate read %s", key)
		}
		seen[key] = true
		if row.Accepted != (row.Error == nil) || row.Accepted != (row.Write != nil) || row.Accepted && (row.Value == nil) != (row.Nullable && row.Input == "null") {
			t.Fatalf("invalid read outcome %s", key)
		}
		if row.Write != nil && (row.Write.Accepted != (row.Write.Error == nil) || row.Write.Accepted != (row.Write.Output != nil)) {
			t.Fatalf("invalid reserialization outcome %s", key)
		}
	}
	seen = map[string]bool{}
	for index, row := range actual.Writes {
		input := corpus.Writes[index]
		key := row.Type + "/" + row.ID
		if seen[key] || row.ID != input.ID || row.Type != input.Type || row.Value != input.Value || row.Accepted != (row.Error == nil) || row.Accepted != (row.Output != nil) {
			t.Fatalf("invalid write observation %s", key)
		}
		seen[key] = true
	}
	return actual
}

func parsers(t *testing.T, fixture profile) map[string]*serialization.Int32Enum[int32] {
	t.Helper()
	result := map[string]*serialization.Int32Enum[int32]{}
	for _, name := range []string{"State", "Access"} {
		members := map[string]int32{}
		for _, m := range fixture.Members[name] {
			value, err := strconv.ParseInt(m.Value, 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			members[m.Name] = int32(value)
		}
		parser, err := serialization.NewInt32Enum(members)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = parser
	}
	return result
}

func TestInt32RuntimeMatchesActualArcReference(t *testing.T) {
	fixture := pinnedProfile(t)
	parsers := parsers(t, fixture)
	count := 0
	for _, row := range fixture.Reads {
		parser, supported := parsers[row.Type]
		if !supported {
			continue // Wide/Tiny/Unsigned are explicitly unimplemented controls.
		}
		count++
		t.Run(row.Type+"/"+row.ID, func(t *testing.T) {
			if row.Nullable && row.Input == "null" {
				var nullable *int32
				if err := serialization.Unmarshal([]byte(row.Input), &nullable); err != nil || nullable != nil || !row.Accepted {
					t.Fatalf("nullable=%v error=%v", nullable, err)
				}
				return
			}
			value, err := parser.ParseJSON([]byte(row.Input))
			if (err == nil) != row.Accepted {
				t.Fatalf("accepted=%v, want %v; error=%v", err == nil, row.Accepted, err)
			}
			if err != nil {
				if !errors.Is(err, serialization.ErrInvalidEnumValue) || value != 0 {
					t.Fatalf("failure value=%d error=%v", value, err)
				}
				return
			}
			if row.Value == nil || strconv.FormatInt(int64(value), 10) != *row.Value {
				t.Fatalf("value=%d want=%v", value, row.Value)
			}
			wire, err := serialization.Marshal(value)
			if err != nil || !row.Write.Accepted || string(wire) != *row.Write.Output {
				t.Fatalf("wire=%s error=%v", wire, err)
			}
		})
	}
	if count != 65 {
		t.Fatalf("Int32 read inventory=%d", count)
	}
	count = 0
	for _, row := range fixture.Writes {
		if _, supported := parsers[row.Type]; !supported {
			continue
		}
		count++
		t.Run("write/"+row.Type+"/"+row.ID, func(t *testing.T) {
			value, err := strconv.ParseInt(row.Value, 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := serialization.Marshal(int32(value))
			if err != nil || !row.Accepted || string(wire) != *row.Output {
				t.Fatalf("wire=%s error=%v", wire, err)
			}
		})
	}
	if count != 8 {
		t.Fatalf("Int32 write inventory=%d", count)
	}
}

func TestNormalizedFactsPreserveOpenOutputAndOriginalParseNames(t *testing.T) {
	fixture := pinnedProfile(t)
	var normalized struct {
		OrdinaryGoEnumDomain, NumberInput, StringInput string
		OutputSchema                                   map[string]any
		Enums                                          []struct {
			Name    string
			Members []struct{ ParseName, ExportName, Value string }
		}
	}
	loadJSON(t, "normalized.json", &normalized)
	if normalized.OrdinaryGoEnumDomain != "open-underlying-integer" || normalized.NumberInput != "declared-values-only" || normalized.StringInput != "original-names-or-name-combinations-or-any-int32-decimal" {
		t.Fatal("enum domains were conflated")
	}
	if !reflect.DeepEqual(normalized.OutputSchema, map[string]any{"type": "integer", "minimum": float64(math.MinInt32), "maximum": float64(math.MaxInt32)}) {
		t.Fatal("output schema must admit unknown Int32 values, not an enum constant list")
	}
	if len(normalized.Enums) != 2 {
		t.Fatal("incomplete normalized inventory")
	}
	for _, enum := range normalized.Enums {
		var members []member
		for _, m := range enum.Members {
			members = append(members, member{m.ParseName, m.Value})
			if m.ParseName == "Read" && m.ExportName != "reader" {
				t.Fatal("fixture must exercise a TypeScript-only rename")
			}
		}
		if !reflect.DeepEqual(members, fixture.Members[enum.Name]) {
			t.Fatalf("original names/aliases/unsigned member ordering lost for %s", enum.Name)
		}
	}
}
