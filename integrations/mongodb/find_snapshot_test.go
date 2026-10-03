// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestFindDetachmentPreservesBSON(t *testing.T) {
	decimal, err := bson.ParseDecimal128("123.450")
	if err != nil {
		t.Fatal(err)
	}
	filter := bson.D{
		{Key: "int32", Value: int32(1)}, {Key: "int64", Value: int64(1)},
		{Key: "nested", Value: bson.D{{Key: "duplicate", Value: int32(2)}, {Key: "duplicate", Value: int64(3)}}},
		{Key: "array", Value: bson.A{int32(4), int64(5), bson.D{{Key: "child", Value: "value"}}}},
		{Key: "binary", Value: bson.Binary{Subtype: 0x80, Data: []byte{0, 1, 255}}},
		{Key: "id", Value: bson.NewObjectID()}, {Key: "decimal", Value: decimal},
		{Key: "date", Value: bson.DateTime(1234)}, {Key: "timestamp", Value: bson.Timestamp{T: 1, I: 2}},
		{Key: "regex", Value: bson.Regex{Pattern: "a.*", Options: "i"}},
		{Key: "code", Value: bson.CodeWithScope{Code: "return x", Scope: bson.D{{Key: "x", Value: int64(6)}}}},
		{Key: "pointer", Value: bson.DBPointer{DB: "db", Pointer: bson.NewObjectID()}},
		{Key: "javascript", Value: bson.JavaScript("1")}, {Key: "symbol", Value: bson.Symbol("s")},
		{Key: "undefined", Value: bson.Undefined{}}, {Key: "null", Value: nil},
		{Key: "min", Value: bson.MinKey{}}, {Key: "max", Value: bson.MaxKey{}},
		{Key: "double", Value: float64(1.25)}, {Key: "bool", Value: true},
	}
	registry, err := NewRegistry(reflect.TypeFor[author]())
	if err != nil {
		t.Fatal(err)
	}
	before, err := freezeFilter(registry, filter)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(Find[author]{Filter: filter})
	if err != nil {
		t.Fatal(err)
	}
	var detached Find[author]
	if err := json.Unmarshal(data, &detached); err != nil {
		t.Fatal(err)
	}
	after, err := freezeFilter(registry, detached.Filter)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("BSON changed: %x -> %x (%v)", before, after, err)
	}
	filter[0].Value = int32(99)
	filter[4].Value.(bson.Binary).Data[0] = 77
	again, err := freezeFilter(registry, detached.Filter)
	if err != nil || !bytes.Equal(before, again) {
		t.Fatal("detached filter aliases authored graph")
	}
}

func TestFindDecodeFailureIsAtomic(t *testing.T) {
	for _, input := range []string{`null`, `{}`, `{"bson":null}`, `{"bson":"!"}`, `{"bson":"AQID"}`, `{"bson":"BQAAAAAAAA=="}`, `{"bson":"BQAAAAA=","extra":true}`, `{"bson":"BQAAAAA="} {}`} {
		q := Find[author]{Filter: bson.D{{Key: "keep", Value: int64(9)}}}
		before := q.Filter
		if err := json.Unmarshal([]byte(input), &q); err == nil || !reflect.DeepEqual(before, q.Filter) {
			t.Fatalf("invalid input accepted or destination changed: %s (%v)", input, err)
		}
	}
	var nilFind *Find[author]
	if !errors.Is(nilFind.UnmarshalJSON([]byte(`{}`)), ErrValue) {
		t.Fatal("nil receiver")
	}
	data, err := json.Marshal(Find[author]{})
	if err != nil {
		t.Fatal(err)
	}
	var q Find[author]
	if err := json.Unmarshal(data, &q); err != nil || q.Filter == nil || len(q.Filter) != 0 {
		t.Fatalf("empty predicate: %+v %v", q, err)
	}
}

func FuzzFindEnvelope(f *testing.F) {
	f.Add([]byte(`{"bson":"BQAAAAA="}`))
	f.Add([]byte(`{"bson":"!"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		q := Find[author]{Filter: bson.D{{Key: "keep", Value: int32(7)}}}
		before := q.Filter
		if err := json.Unmarshal(input, &q); err != nil && !reflect.DeepEqual(before, q.Filter) {
			t.Fatal("failed decode changed destination")
		}
	})
}
