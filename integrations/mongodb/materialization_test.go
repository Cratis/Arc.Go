// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type calendarConcept[T any] struct{ scalar T }

func (calendarConcept[T]) ConceptValue() T                    { panic("must not call") }
func (v calendarConcept[T]) MarshalJSON() ([]byte, error)     { return json.Marshal(v.scalar) }
func (v *calendarConcept[T]) UnmarshalJSON(data []byte) error { return json.Unmarshal(data, &v.scalar) }
func (v calendarConcept[T]) MarshalText() ([]byte, error) {
	return any(v.scalar).(encoding.TextMarshaler).MarshalText()
}
func (v *calendarConcept[T]) UnmarshalText(data []byte) error {
	return any(&v.scalar).(encoding.TextUnmarshaler).UnmarshalText(data)
}

func TestSharedScalarConcepts(t *testing.T) {
	for _, tc := range []struct {
		value   any
		input   string
		fixture string
	}{
		{valueBox[calendarConcept[concepts.UUID]]{}, `"00112233-4455-6677-8899-aabbccddeeff"`, "1d000000057600100000000400112233445566778899aabbccddeeff00"},
		{valueBox[calendarConcept[concepts.DateOnly]]{}, `"2024-02-29"`, "100000000976000056bcf48d01000000"},
		{valueBox[calendarConcept[concepts.TimeOnly]]{}, `"12:34:56.7890000"`, "10000000097600952cb3020000000000"},
	} {
		t.Run(reflect.TypeOf(tc.value).String(), func(t *testing.T) {
			r := registryFor(t, tc.value)
			value := reflect.New(reflect.TypeOf(tc.value))
			if err := json.Unmarshal([]byte(`{"v":`+tc.input+`}`), value.Interface()); err != nil {
				t.Fatal(err)
			}
			wire, err := encode(r, value.Elem().Interface())
			if err != nil || !bytes.Equal(wire, literal(t, tc.fixture)) {
				t.Fatalf("wire %x error %v", wire, err)
			}
			got := reflect.New(reflect.TypeOf(tc.value))
			if err := decode(r, literal(t, tc.fixture), got.Interface()); err != nil || !reflect.DeepEqual(got.Interface(), value.Interface()) {
				t.Fatalf("got %v error %v", got, err)
			}
		})
	}
	span, err := concepts.ParseTimeSpan("-1.02:03:04.0000005")
	if err != nil {
		t.Fatal(err)
	}
	value := valueBox[calendarConcept[concepts.TimeSpan]]{Value: calendarConcept[concepts.TimeSpan]{scalar: span}}
	r := registryFor(t, value)
	wire, err := encode(r, value)
	if err != nil {
		t.Fatal(err)
	}
	if bson.Raw(wire).Lookup("v").StringValue() != "-1.02:03:04.0000005" {
		t.Fatal("TimeSpan is not invariant text")
	}
	var got valueBox[calendarConcept[concepts.TimeSpan]]
	if err := decode(r, wire, &got); err != nil || got != value {
		t.Fatalf("got %v error %v", got, err)
	}
}

func TestNestedMaterializationAndOmission(t *testing.T) {
	type child struct {
		Name   string  `json:"name"`
		Values []int32 `json:"values"`
	}
	type model struct {
		ID       int32    `json:"id" bson:"_id"`
		Child    child    `json:"child"`
		Children []*child `json:"children"`
		Optional string   `json:"optional,omitempty"`
	}
	wire, err := bson.Marshal(bson.D{
		{Key: "_id", Value: int32(0)},
		{Key: "child", Value: bson.D{{Key: "name", Value: "zero"}}},
		{Key: "children", Value: bson.A{bson.D{{Key: "name", Value: "one"}, {Key: "values", Value: nil}}, nil}},
		{Key: "unknown", Value: bson.D{{Key: "ignored", Value: 42}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got model
	r := registryFor(t, got)
	if err := decode(r, wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Child.Values == nil || len(got.Children) != 2 || got.Children[0].Values == nil || got.Children[1] != nil {
		t.Fatalf("materialization %v", got)
	}
	written, err := encode(r, got)
	if err != nil {
		t.Fatal(err)
	}
	if bson.Raw(written).Lookup("optional").Type != 0 {
		t.Fatal("omission ignored")
	}
	before := got
	if err := decode(r, literal(t, "080000000a760000"), &got); !errors.Is(err, mongodb.ErrValue) {
		t.Fatal("missing id accepted")
	}
	if !reflect.DeepEqual(got, before) {
		t.Fatal("failed decode changed nested state")
	}
}

func TestScalarWriteLimits(t *testing.T) {
	for _, value := range []any{valueBox[int]{Value: math.MaxInt32 + 1}, valueBox[float64]{Value: math.NaN()}, valueBox[float64]{Value: math.Inf(-1)}, valueBox[string]{Value: "bad\xff"}} {
		if _, err := encode(registryFor(t, value), value); !errors.Is(err, mongodb.ErrValue) {
			t.Fatalf("%T: %v", value, err)
		}
	}
	for _, value := range []any{valueBox[int8]{Value: -128}, valueBox[uint8]{Value: 255}, valueBox[uint32]{Value: math.MaxUint32}, valueBox[uint64]{Value: math.MaxUint64}} {
		r := registryFor(t, value)
		wire, err := encode(r, value)
		if err != nil {
			t.Fatal(err)
		}
		got := reflect.New(reflect.TypeOf(value))
		if err := decode(r, wire, got.Interface()); err != nil || !reflect.DeepEqual(got.Elem().Interface(), value) {
			t.Fatalf("got %v error %v", got, err)
		}
	}
}

func TestDoubleNegativeZero(t *testing.T) {
	value := valueBox[float64]{Value: math.Copysign(0, -1)}
	r := registryFor(t, value)
	wire := literal(t, "10000000017600000000000000008000")
	written, err := encode(r, value)
	if err != nil || !bytes.Equal(written, wire) {
		t.Fatalf("wire %x, error %v", written, err)
	}
	var got valueBox[float64]
	if err := decode(r, wire, &got); err != nil || !math.Signbit(got.Value) {
		t.Fatalf("got %v, error %v", got, err)
	}
}

func TestDiscoveryGraphLimits(t *testing.T) {
	var types []reflect.Type
	for mask := 0; mask < 128; mask++ {
		typeOf := reflect.TypeFor[int32]()
		for bit := 0; bit < 7; bit++ {
			if mask&(1<<bit) == 0 {
				typeOf = reflect.PointerTo(typeOf)
			} else {
				typeOf = reflect.SliceOf(typeOf)
			}
		}
		types = append(types, typeOf)
	}
	// The complete seven-level binary graph has 255 distinct types. One
	// additional scalar fills the limit; repeated types must remain admissible.
	types = append(types, reflect.TypeFor[bool](), reflect.TypeFor[bool]())
	if _, err := mongodb.NewRegistry(types...); err != nil {
		t.Fatal(err)
	}
	if _, err := mongodb.NewRegistry(append(types, reflect.TypeFor[uint8]())...); !errors.Is(err, mongodb.ErrUnsupportedModel) {
		t.Fatal("graph limit was not enforced")
	}
	deep := reflect.TypeFor[int32]()
	for i := 0; i < 64; i++ {
		deep = reflect.PointerTo(deep)
	}
	if _, err := mongodb.NewRegistry(deep); err != nil {
		t.Fatal(err)
	}
	if _, err := mongodb.NewRegistry(reflect.PointerTo(deep)); !errors.Is(err, mongodb.ErrUnsupportedModel) {
		t.Fatal("depth limit was not enforced")
	}
}

func ExampleNewRegistry() {
	type author struct {
		ID   concepts.UUID `json:"id" bson:"_id"`
		Name string        `json:"name"`
	}
	registry, err := mongodb.NewRegistry(reflect.TypeFor[author]())
	if err != nil {
		fmt.Println(err)
		return
	}
	id, err := concepts.ParseUUID("00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		fmt.Println(err)
		return
	}
	var buffer bytes.Buffer
	encoder := bson.NewEncoder(bson.NewDocumentWriter(&buffer))
	encoder.SetRegistry(registry)
	if err := encoder.Encode(author{ID: id, Name: "Ada"}); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(bson.Raw(buffer.Bytes()).Lookup("name").StringValue())
	// Output: Ada
}
