// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

type state int32

var stateCodec, stateCodecError = serialization.NewInt32Enum(map[string]state{"None": 0, "Read": 1, "Write": 4, "Alias": 4})

func (s *state) UnmarshalJSON(data []byte) error {
	if stateCodecError != nil {
		return stateCodecError
	}
	value, err := stateCodec.ParseJSON(data)
	if err != nil {
		return err
	}
	*s = value
	return nil
}

func ExampleInt32Enum() {
	var input struct{ State state }
	if err := serialization.Unmarshal([]byte(`{"state":"Read, Write"}`), &input); err != nil {
		fmt.Println(err)
		return
	}
	wire, err := serialization.Marshal(input)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(wire))
	// Output: {"state":5}
}

func TestInt32EnumRejectsInvalidDeclarations(t *testing.T) {
	for name, members := range map[string]map[string]int32{
		"nil": nil, "empty": {}, "blank": {"": 1}, "space": {" Read": 1},
		"digit": {"1Read": 1}, "comma": {"Read,Write": 1}, "unicode": {"Réad": 1},
		"case collision": {"Read": 1, "READ": 2},
	} {
		t.Run(name, func(t *testing.T) {
			parser, err := serialization.NewInt32Enum(members)
			if err == nil || parser != nil {
				t.Fatalf("parser=%v error=%v", parser, err)
			}
		})
	}
}

func TestInt32EnumOwnsItsDeclaration(t *testing.T) {
	members := map[string]int32{"Read": 1, "Alias": 1}
	parser, err := serialization.NewInt32Enum(members)
	if err != nil {
		t.Fatal(err)
	}
	members["Read"] = 7
	delete(members, "Alias")
	for _, input := range []string{`"read"`, `"ALIAS"`, `1`} {
		got, err := parser.ParseJSON([]byte(input))
		if err != nil || got != 1 {
			t.Fatalf("%s: got=%d error=%v", input, got, err)
		}
	}
}

func TestInt32EnumZeroAndNilRejectInput(t *testing.T) {
	for _, parser := range []*serialization.Int32Enum[int32]{nil, {}} {
		if _, err := parser.ParseJSON([]byte(`0`)); !errors.Is(err, serialization.ErrInvalidEnumValue) {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestInt32EnumFailureDoesNotMutateCaller(t *testing.T) {
	for _, input := range []string{`{"state":"Read,Missing"}`, `{"state":5}`, `{"state":null}`} {
		target := struct{ State state }{State: 4}
		if err := serialization.Unmarshal([]byte(input), &target); !errors.Is(err, serialization.ErrInvalidEnumValue) {
			t.Fatalf("error=%v", err)
		}
		if target.State != 4 {
			t.Fatal("failed parsing changed target")
		}
	}
}

func TestInt32EnumNullPresenceAndCollections(t *testing.T) {
	var target struct {
		Pointer  *state
		Optional serialization.Optional[state]
		Values   []state
	}
	if err := serialization.Unmarshal([]byte(`{"pointer":null,"optional":null,"values":["Read","Alias","5"]}`), &target); err != nil {
		t.Fatal(err)
	}
	wire, err := serialization.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) != `{"optional":null,"values":[1,4,5]}` {
		t.Fatalf("wire=%s", wire)
	}
	if err := serialization.Unmarshal([]byte(`{"values":[null]}`), &target); !errors.Is(err, serialization.ErrInvalidEnumValue) {
		t.Fatalf("nonnullable enum element error=%v", err)
	}
}

func TestInt32EnumDoesNotNarrowOrdinaryIntegerDomains(t *testing.T) {
	type ordinary int32
	for _, value := range []ordinary{23, 5, math.MinInt32, math.MaxInt32} {
		wire, err := serialization.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var result ordinary
		if err := serialization.Unmarshal(wire, &result); err != nil || result != value {
			t.Fatalf("value=%d result=%d error=%v", value, result, err)
		}
	}
	type wide uint64
	var result wide
	if err := serialization.Unmarshal([]byte(`18446744073709551615`), &result); err != nil || result != math.MaxUint64 {
		t.Fatalf("wide=%d error=%v", result, err)
	}
}

func TestInt32EnumConcurrentParsing(t *testing.T) {
	if stateCodecError != nil {
		t.Fatal(stateCodecError)
	}
	for range 8 {
		t.Run("shared", func(t *testing.T) {
			t.Parallel()
			value, err := stateCodec.ParseJSON([]byte(`"Read,Alias"`))
			if err != nil || value != 5 {
				t.Fatalf("value=%d error=%v", value, err)
			}
		})
	}
}

func FuzzInt32Enum(f *testing.F) {
	for _, seed := range []string{`null`, `4`, `"Read, Write"`, `"+23"`, `"2147483648"`, `"23\u0000"`, `"Missing"`, `"Read,,Write"`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_, err := stateCodec.ParseJSON([]byte(input))
		if err != nil && !errors.Is(err, serialization.ErrInvalidEnumValue) {
			t.Fatalf("unexpected error=%v", err)
		}
	})
}
