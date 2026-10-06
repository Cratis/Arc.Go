// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/serialization"
)

type input struct {
	Name   serialization.Optional[string]
	Count  serialization.Optional[int]
	Active serialization.Optional[bool]
}

func TestPresenceAwareBinding(t *testing.T) {
	for _, tc := range []struct {
		name, json    string
		present, null bool
		value         string
	}{
		{"missing", `{}`, false, false, ""}, {"null", `{"name":null}`, true, true, ""}, {"empty", `{"name":""}`, true, false, ""}, {"value", `{"name":"Ada","unknown":17}`, true, false, "Ada"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := input{Name: serialization.Some("stale")}
			if err := serialization.Unmarshal([]byte(tc.json), &value); err != nil {
				t.Fatal(err)
			}
			got, ok := value.Name.Value()
			if value.Name.IsPresent() != tc.present || value.Name.IsNull() != tc.null || ok != (tc.present && !tc.null) || got != tc.value {
				t.Fatalf("presence = %#v (%s, %v)", value.Name, got, ok)
			}
			if value.Name.IsZero() == tc.present {
				t.Fatal("IsZero disagrees with presence")
			}
			encoded, err := serialization.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			expected := `{}`
			if tc.null {
				expected = `{"name":null}`
			} else if tc.present {
				b, err := json.Marshal(tc.value)
				if err != nil {
					t.Fatal(err)
				}
				expected = `{"name":` + string(b) + `}`
			}
			if string(encoded) != expected {
				t.Fatalf("JSON = %s, want %s", encoded, expected)
			}
		})
	}
	var value input
	if err := serialization.Unmarshal([]byte(`{"count":0,"active":false}`), &value); err != nil {
		t.Fatal(err)
	}
	data, err := serialization.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"active":false,"count":0}` {
		t.Fatalf("lost scalar zero: %s", data)
	}
}

func TestDuplicateDeclaredKeysAndAtomicFailure(t *testing.T) {
	value := input{Name: serialization.Some("original")}
	err := serialization.Unmarshal([]byte(`{"name":"first","name":"second"}`), &value)
	var duplicate *serialization.DuplicateMemberError
	if !errors.As(err, &duplicate) || duplicate.Member != "name" {
		t.Fatalf("error = %v", err)
	}
	if got, _ := value.Name.Value(); got != "original" {
		t.Fatal("target mutated on failure")
	}
	if err = serialization.Unmarshal([]byte(`{"unknown":1,"UNKNOWN":2}`), &value); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`[]`, `null`, `{} {}`, `{"name":4}`, `{"name":`, `{"count":1.1}`} {
		if err = serialization.Unmarshal([]byte(bad), &value); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	for _, target := range []any{nil, input{}, (*input)(nil)} {
		if err = serialization.Unmarshal([]byte(`{}`), target); err == nil {
			t.Errorf("accepted target %T", target)
		}
	}
}

func TestArcNamingNullsAndNumericLiterals(t *testing.T) {
	for name, want := range map[string]string{"": "", "Name": "name", "ID": "ID", "URLValue": "URLValue", "MyURL": "myURL", "already": "already"} {
		if got := serialization.CamelCase(name); got != want {
			t.Errorf("%s -> %s, want %s", name, got, want)
		}
	}
	var nilString *string
	value := struct {
		URLValue     string
		ID           int `json:"id"`
		Omitted      any
		Zero         int
		Name         *string
		Empty        []string
		Missing      serialization.Optional[int]
		ExplicitNull serialization.Optional[int]
	}{
		URLValue: "abc", Omitted: nilString, Empty: []string{}, ExplicitNull: serialization.Null[int](),
	}
	data, err := serialization.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"URLValue":"abc","empty":[],"explicitNull":null,"id":0,"zero":0}` {
		t.Fatalf("JSON = %s", data)
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		encoded, err := serialization.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var decoded float64
		if err = serialization.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if !(math.IsNaN(v) && math.IsNaN(decoded)) && v != decoded {
			t.Fatalf("float round trip: %s", encoded)
		}
	}
}

func TestNestedBindingAndCollections(t *testing.T) {
	type item struct{ Name string }
	type model struct {
		Items    []item
		Lookup   map[string]*item
		Flags    [2]bool
		Optional *item
	}
	var value model
	data := []byte(`{"items":[{"name":"Ada"}],"lookup":{"one":{"name":"Grace"},"absent":null},"flags":[true,false],"optional":{"name":"Linus"}}`)
	if err := serialization.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value.Items[0].Name != "Ada" || value.Lookup["one"].Name != "Grace" || value.Lookup["absent"] != nil || value.Optional.Name != "Linus" {
		t.Fatalf("bound = %#v", value)
	}
	encoded, err := serialization.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip model
	if err = serialization.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, roundTrip) {
		t.Fatal("nested round trip changed value")
	}
	if err = serialization.Unmarshal([]byte(`{"items":[{"name":"a","name":"b"}]}`), &value); err == nil {
		t.Fatal("nested duplicate accepted")
	}
}

func TestUnsupportedShapesAndCyclesFail(t *testing.T) {
	type cycle struct{ Next *cycle }
	loop := &cycle{}
	loop.Next = loop
	// Construct the deliberately invalid tag collision dynamically so go vet can
	// still enforce unique JSON tags on the repository's ordinary DTO fixtures.
	ambiguous := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "First", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
		{Name: "Second", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
	})).Interface()
	for _, value := range []any{map[int]int{1: 2}, ambiguous, struct {
		Name string `json:",string"`
	}{}, loop, make(chan int)} {
		if _, err := serialization.Marshal(value); err == nil {
			t.Errorf("encoded unsupported %T", value)
		}
	}
	var scalar int
	if err := serialization.Unmarshal([]byte(`null`), &scalar); err == nil {
		t.Fatal("non-nullable scalar accepted null")
	}
}

func FuzzPresenceBinding(f *testing.F) {
	f.Add(`{"name":"Ada"}`)
	f.Add(`{"name":null}`)
	f.Add(`{}`)
	f.Fuzz(func(t *testing.T, data string) {
		var value input
		if err := serialization.Unmarshal([]byte(data), &value); err != nil {
			return
		}
		encoded, err := serialization.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded input
		if err = serialization.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, decoded) {
			t.Fatal("presence round trip changed value")
		}
	})
}
