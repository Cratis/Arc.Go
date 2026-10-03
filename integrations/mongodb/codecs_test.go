// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type amount int32

func (amount) ConceptValue() int32            { panic("discovery/encoding must not call ConceptValue") }
func (v amount) MarshalJSON() ([]byte, error) { return json.Marshal(int32(v)) }
func (v *amount) UnmarshalJSON(data []byte) error {
	var scalar int32
	if err := json.Unmarshal(data, &scalar); err != nil {
		return err
	}
	*v = amount(scalar)
	return nil
}
func (v amount) MarshalText() ([]byte, error) { return []byte(strconv.FormatInt(int64(v), 10)), nil }
func (v *amount) UnmarshalText(data []byte) error {
	scalar, err := strconv.ParseInt(string(data), 10, 32)
	if err != nil {
		return err
	}
	*v = amount(scalar)
	return nil
}

type valueBox[T any] struct {
	Value T `json:"v"`
}

func encode(registry *bson.Registry, value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := bson.NewEncoder(bson.NewDocumentWriter(&buffer))
	encoder.SetRegistry(registry)
	err := encoder.Encode(value)
	return buffer.Bytes(), err
}

func decode(registry *bson.Registry, data []byte, target any) error {
	decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(data)))
	decoder.SetRegistry(registry)
	return decoder.Decode(target)
}

func registryFor(t *testing.T, value any) *bson.Registry {
	t.Helper()
	r, err := mongodb.NewRegistry(reflect.TypeOf(value))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func literal(t *testing.T, text string) []byte {
	t.Helper()
	data, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestIndependentFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/bson/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Hex   string `json:"hex"`
		JSON  string `json:"json"`
		Write bool   `json:"write"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 19 {
		t.Fatalf("fixture count = %d, want 19", len(fixtures))
	}
	types := map[string]reflect.Type{
		"int32": reflect.TypeFor[valueBox[int32]](), "int64": reflect.TypeFor[valueBox[int64]](),
		"float64": reflect.TypeFor[valueBox[float64]](), "bool": reflect.TypeFor[valueBox[bool]](),
		"string": reflect.TypeFor[valueBox[string]](), "uuid": reflect.TypeFor[valueBox[concepts.UUID]](),
		"amount": reflect.TypeFor[valueBox[amount]](), "date": reflect.TypeFor[valueBox[concepts.DateOnly]](),
		"clock": reflect.TypeFor[valueBox[concepts.TimeOnly]](), "time": reflect.TypeFor[valueBox[time.Time]](),
		"slice": reflect.TypeFor[valueBox[[]int32]](), "pointer": reflect.TypeFor[valueBox[*int32]](),
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			typeOf := types[f.Kind]
			if typeOf == nil {
				t.Fatalf("unknown fixture kind %q", f.Kind)
			}
			r, err := mongodb.NewRegistry(typeOf)
			if err != nil {
				t.Fatal(err)
			}
			want := reflect.New(typeOf)
			if err := json.Unmarshal([]byte(`{"v":`+f.JSON+`}`), want.Interface()); err != nil {
				t.Fatal(err)
			}
			got := reflect.New(typeOf)
			wire := literal(t, f.Hex)
			if err := decode(r, wire, got.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Interface(), want.Interface()) {
				t.Fatalf("got %v, want %v", got, want)
			}
			if f.Write {
				written, err := encode(r, want.Elem().Interface())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(written, wire) {
					t.Fatalf("write = %x, want %x", written, wire)
				}
			}
		})
	}
}

func TestCheckedNumbers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  any
		target any
		ok     bool
	}{
		{"int32-to-int8", int32(127), valueBox[int8]{}, true},
		{"int8-overflow", int32(128), valueBox[int8]{}, false},
		{"negative-unsigned", int64(-1), valueBox[uint64]{}, false},
		{"double-integral", float64(42), valueBox[amount]{}, true},
		{"double-fraction", 42.5, valueBox[amount]{}, false},
		{"double-int64-overflow", float64(9223372036854775808.0), valueBox[int64]{}, false},
		{"nan", math.NaN(), valueBox[float64]{}, false},
		{"infinity", math.Inf(1), valueBox[float64]{}, false},
		{"float32-overflow", math.MaxFloat64, valueBox[float32]{}, false},
		{"uint64-max-string", "18446744073709551615", valueBox[uint64]{}, true},
		{"uint64-overflow-string", "18446744073709551616", valueBox[uint64]{}, false},
		{"int64-max-string", "9223372036854775807", valueBox[int64]{}, true},
		{"exponent-integral", "4.2e1", valueBox[amount]{}, true},
		{"fraction-string", "0.01", valueBox[int32]{}, false},
		{"ratio-string", "1/2", valueBox[float64]{}, false},
		{"huge-exponent", "1e999999999", valueBox[float64]{}, false},
		{"whitespace-string", " 42", valueBox[amount]{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := registryFor(t, tc.target)
			wire, err := bson.Marshal(bson.D{{Key: "v", Value: tc.input}})
			if err != nil {
				t.Fatal(err)
			}
			value := reflect.New(reflect.TypeOf(tc.target))
			err = decode(r, wire, value.Interface())
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && !errors.Is(err, mongodb.ErrValue) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	decimal, err := bson.ParseDecimal128("18446744073709551615")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := bson.Marshal(bson.D{{Key: "v", Value: decimal}})
	if err != nil {
		t.Fatal(err)
	}
	var value valueBox[uint64]
	r := registryFor(t, value)
	if err := decode(r, wire, &value); err != nil || value.Value != math.MaxUint64 {
		t.Fatalf("value %v, error %v", value, err)
	}
	written, err := encode(r, value)
	if err != nil || !bytes.Equal(wire, written) {
		t.Fatalf("Decimal128 write %x, error %v", written, err)
	}
}

func TestInvalidValuesLeaveDestinationUnchanged(t *testing.T) {
	for _, wire := range []string{
		"080000000a760000",                 // scalar null
		"0c0000001076002a000000",           // truncated document
		"10000000017600000000000040454000", // fractional double
		"20000000037600180000001056616c7565002a000000107800010000000000", // legacy extra field (malformed length is still a rejection)
	} {
		t.Run(wire, func(t *testing.T) {
			value := valueBox[amount]{Value: 7}
			if err := decode(registryFor(t, value), literal(t, wire), &value); !errors.Is(err, mongodb.ErrValue) {
				t.Fatalf("error = %v", err)
			}
			if value.Value != 7 {
				t.Fatalf("changed destination: %v", value)
			}
		})
	}
	type model struct {
		ID    int32   `json:"id" bson:"_id"`
		Items []int32 `json:"items"`
		Other *int32  `json:"other"`
	}
	other := int32(8)
	value := model{ID: 7, Items: []int32{9}, Other: &other}
	before := model{ID: 7, Items: []int32{9}, Other: &other}
	r := registryFor(t, value)
	wire, err := bson.Marshal(bson.D{{Key: "_id", Value: int32(1)}, {Key: "items", Value: bson.A{int32(2), "bad"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(r, wire, &value); !errors.Is(err, mongodb.ErrValue) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, before) || other != 8 {
		t.Fatal("nested destination changed")
	}
	for _, document := range []bson.D{
		{}, {{Key: "_id", Value: nil}}, {{Key: "_id", Value: int32(1)}, {Key: "_id", Value: int32(2)}},
	} {
		wire, err := bson.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := decode(r, wire, &value); !errors.Is(err, mongodb.ErrValue) {
			t.Fatalf("identity rejection = %v", err)
		}
		if !reflect.DeepEqual(value, before) {
			t.Fatal("identity failure changed destination")
		}
	}
}

func TestLegacyConceptShapeIsStrict(t *testing.T) {
	r := registryFor(t, valueBox[amount]{})
	for _, legacy := range []bson.D{
		{}, {{Key: "wrong", Value: int32(1)}}, {{Key: "Value", Value: nil}},
		{{Key: "Value", Value: int32(1)}, {Key: "value", Value: int32(1)}},
		{{Key: "Value", Value: int32(1)}, {Key: "Value", Value: int32(1)}},
	} {
		wire, err := bson.Marshal(bson.D{{Key: "v", Value: legacy}})
		if err != nil {
			t.Fatal(err)
		}
		value := valueBox[amount]{Value: 9}
		if err := decode(r, wire, &value); !errors.Is(err, mongodb.ErrValue) || value.Value != 9 {
			t.Fatalf("value %v, error %v", value, err)
		}
	}
}

func TestUUIDRepresentations(t *testing.T) {
	r := registryFor(t, valueBox[concepts.UUID]{})
	for _, tc := range []struct {
		input any
		ok    bool
	}{
		{"00112233-4455-6677-8899-AABBCCDDEEFF", true},
		{bson.Binary{Subtype: 3, Data: make([]byte, 16)}, false},
		{bson.Binary{Subtype: 4, Data: make([]byte, 15)}, false},
		{"00112233445566778899aabbccddeeff", false},
	} {
		wire, err := bson.Marshal(bson.D{{Key: "v", Value: tc.input}})
		if err != nil {
			t.Fatal(err)
		}
		var value valueBox[concepts.UUID]
		err = decode(r, wire, &value)
		if tc.ok && err != nil {
			t.Fatal(err)
		}
		if !tc.ok && !errors.Is(err, mongodb.ErrValue) {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestTemporalPrecisionAndPointers(t *testing.T) {
	clock, err := concepts.ParseTimeOnly("12:34:56.7899999")
	if err != nil {
		t.Fatal(err)
	}
	value := valueBox[concepts.TimeOnly]{Value: clock}
	r := registryFor(t, value)
	wire, err := encode(r, value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire, literal(t, "10000000097600952cb3020000000000")) {
		t.Fatalf("clock = %x", wire)
	}
	timestamp := time.Date(1969, 12, 31, 23, 59, 59, 999999999, time.UTC).In(time.FixedZone("offset", 3600))
	v := valueBox[time.Time]{Value: timestamp}
	r = registryFor(t, v)
	wire, err = encode(r, v)
	if err != nil {
		t.Fatal(err)
	}
	var got valueBox[time.Time]
	if err := decode(r, wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Value.Location() != time.UTC || !got.Value.Equal(time.UnixMilli(-1)) {
		t.Fatal(got)
	}
	type nullable struct {
		V *[]int32 `json:"v"`
	}
	r = registryFor(t, nullable{})
	for _, text := range []string{"0500000000", "080000000a760000"} {
		slice := []int32{7}
		got := nullable{V: &slice}
		if err := decode(r, literal(t, text), &got); err != nil || got.V != nil {
			t.Fatalf("value %v, error %v", got, err)
		}
	}
	var empty nullable
	if err := decode(r, literal(t, "0d000000047600050000000000"), &empty); err != nil || empty.V == nil || *empty.V == nil {
		t.Fatalf("value %v, error %v", empty, err)
	}
}

func TestUnsupportedMapping(t *testing.T) {
	type noJSON struct{ V int32 }
	type emptyBSON struct {
		V int32 `json:"v" bson:""`
	}
	type duplicateBSON struct {
		A int32 `json:"a" bson:"v"`
		B int32 `json:"b" bson:"v"`
	}
	type inline struct {
		V int32 `json:"v" bson:",inline"`
	}
	type excluded struct {
		V int32 `json:"-" bson:"-"`
	}
	type nested struct{ noJSON }
	type optional struct {
		V serialization.Optional[int32] `json:"v"`
	}
	type badID struct {
		V int32 `json:"id,omitempty" bson:"_id"`
	}
	type operator struct {
		V int32 `json:"$v"`
	}
	type recursive struct {
		V *recursive `json:"v"`
	}
	for _, model := range []any{noJSON{}, emptyBSON{}, duplicateBSON{}, inline{}, excluded{}, nested{}, optional{}, badID{}, operator{}, recursive{}, valueBox[any]{}, valueBox[map[string]int]{}, valueBox[bson.Decimal128]{}, valueBox[complex128]{}} {
		if _, err := mongodb.NewRegistry(reflect.TypeOf(model)); !errors.Is(err, mongodb.ErrUnsupportedModel) {
			t.Fatalf("%T error = %v", model, err)
		}
	}
	if _, err := mongodb.NewRegistry(); !errors.Is(err, mongodb.ErrUnsupportedModel) {
		t.Fatal(err)
	}
	if _, err := mongodb.NewRegistry(nil); !errors.Is(err, mongodb.ErrUnsupportedModel) {
		t.Fatal(err)
	}
}

func TestIndependentRegistriesAndJSONFallback(t *testing.T) {
	type model struct {
		ID   int32  `json:"id" bson:"_id"`
		Name string `json:"displayName"`
	}
	defaultsBefore, err := bson.Marshal(valueBox[concepts.UUID]{})
	if err != nil {
		t.Fatal(err)
	}
	value := model{ID: 42, Name: "Ada"}
	first := registryFor(t, value)
	second := registryFor(t, value)
	if first == second {
		t.Fatal("shared registry")
	}
	wire, err := encode(first, value)
	if err != nil {
		t.Fatal(err)
	}
	if bson.Raw(wire).Lookup("displayName").StringValue() != "Ada" {
		t.Fatalf("JSON fallback: %x", wire)
	}
	var got model
	if err := decode(second, wire, &got); err != nil || got != value {
		t.Fatalf("got %v error %v", got, err)
	}
	defaults, err := bson.Marshal(valueBox[concepts.UUID]{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(defaults, defaultsBefore) {
		t.Fatal("driver default registry was mutated")
	}
}

type rejectingConcept struct{ value int32 }

func (rejectingConcept) ConceptValue() int32          { panic("must not call") }
func (rejectingConcept) MarshalJSON() ([]byte, error) { return []byte("42"), nil }
func (v *rejectingConcept) UnmarshalJSON([]byte) error {
	v.value = 99
	return errors.New("secret application payload")
}
func (rejectingConcept) MarshalText() ([]byte, error) { return []byte("42"), nil }
func (*rejectingConcept) UnmarshalText([]byte) error  { return errors.New("secret application payload") }

func TestDomainCodecFailureIsAtomicAndRedacted(t *testing.T) {
	value := valueBox[rejectingConcept]{Value: rejectingConcept{value: 7}}
	r := registryFor(t, value) // does not execute the codec
	err := decode(r, literal(t, "0c0000001076002a00000000"), &value)
	if !errors.Is(err, mongodb.ErrValue) || strings.Contains(err.Error(), "secret") || value.Value.value != 7 {
		t.Fatalf("value %v, error %v", value, err)
	}
}

func FuzzDecodeFailureAtomic(f *testing.F) {
	f.Add([]byte{5, 0, 0, 0, 0})
	f.Add([]byte{12, 0, 0, 0, 16, 'v', 0, 42, 0, 0, 0, 0})
	r, err := mongodb.NewRegistry(reflect.TypeFor[valueBox[amount]]())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			return
		}
		value := valueBox[amount]{Value: 7}
		if err := decode(r, data, &value); err != nil && value.Value != 7 {
			t.Fatal("failed decode mutated destination")
		}
	})
}
