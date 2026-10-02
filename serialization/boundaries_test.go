// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

type ordinaryModel struct{ Name string }

func (ordinaryModel) IsPresent() bool { return false }

func TestPresenceMethodDoesNotMakeApplicationModelsOptional(t *testing.T) {
	value := struct{ Model ordinaryModel }{ordinaryModel{Name: "Ada"}}
	data, err := serialization.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"model":{"name":"Ada"}}` {
		t.Fatalf("application model omitted: %s", data)
	}
}

func TestUntypedNumbersKeepTheirPrecision(t *testing.T) {
	input := `{"value":9223372036854775807,"values":[9007199254740993,0.1234567890123456789]}`
	var value map[string]any
	if err := serialization.Unmarshal([]byte(input), &value); err != nil {
		t.Fatal(err)
	}
	if value["value"] != json.Number("9223372036854775807") {
		t.Fatalf("number = %#v", value["value"])
	}
	data, err := serialization.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != input {
		t.Fatalf("precision changed: %s", data)
	}
}

func TestOptionalNestingCannotResetDepthLimit(t *testing.T) {
	type node struct{ Next serialization.Optional[*node] }
	root := &node{}
	current := root
	for range 70 {
		next := &node{}
		current.Next = serialization.Some(next)
		current = next
	}
	if _, err := serialization.Marshal(root); err == nil {
		t.Fatal("optional encoding bypassed depth limit")
	}
	current.Next = serialization.Some(root)
	if _, err := serialization.Marshal(root); err == nil {
		t.Fatal("optional cycle bypassed depth limit")
	}
	data := strings.Repeat(`{"next":`, 70) + `null` + strings.Repeat(`}`, 70)
	var decoded node
	if err := serialization.Unmarshal([]byte(data), &decoded); err == nil {
		t.Fatal("optional binding bypassed depth limit")
	}
	var untyped any
	if err := serialization.Unmarshal([]byte(data), &untyped); err == nil {
		t.Fatal("untyped binding bypassed depth limit")
	}
	var optional serialization.Optional[any]
	if err := json.Unmarshal([]byte(data), &optional); err == nil {
		t.Fatal("direct optional binding bypassed depth limit")
	}
}
