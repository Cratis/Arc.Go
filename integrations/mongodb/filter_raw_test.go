// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/x/bsonx/bsoncore"
)

func TestMalformedNestedBSONIsRejectedAtomically(t *testing.T) {
	seed, err := hex.DecodeString("140000000461000c000000103100070000000000")
	if err != nil {
		t.Fatal(err)
	}
	array := bson.Raw(seed).Lookup("a").Value
	badDocument := []byte{5, 0, 0, 0, 1}
	badString := []byte{2, 0, 0, 0, 'x', 'y'}
	badScope := bsoncore.AppendCodeWithScope(nil, "x", badDocument)
	badCodeString := bsoncore.AppendCodeWithScope(nil, "x", []byte{5, 0, 0, 0, 0})
	badCodeString[9] = 1
	badCodeLength := bytes.Clone(badCodeString)
	badCodeLength[0]--
	cases := []struct {
		name  string
		value bson.RawValue
	}{
		{"first array key one", bson.RawValue{Type: bson.TypeArray, Value: array}},
		{"noncanonical array key", bson.RawValue{Type: bson.TypeArray, Value: bsoncore.BuildDocument(nil, bsoncore.AppendInt32Element(nil, "00", 7))}},
		{"array key gap", bson.RawValue{Type: bson.TypeArray, Value: bsoncore.BuildDocument(nil, bsoncore.AppendInt32Element(nil, "0", 7), bsoncore.AppendInt32Element(nil, "2", 8))}},
		{"nested document terminator", bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: badDocument}},
		{"string terminator", bson.RawValue{Type: bson.TypeString, Value: badString}},
		{"scope document terminator", bson.RawValue{Type: bson.TypeCodeWithScope, Value: badScope}},
		{"scope code terminator", bson.RawValue{Type: bson.TypeCodeWithScope, Value: badCodeString}},
		{"scope total length", bson.RawValue{Type: bson.TypeCodeWithScope, Value: badCodeLength}},
		{"boolean normalization", bson.RawValue{Type: bson.TypeBoolean, Value: []byte{2}}},
		{"raw value trailing bytes", bson.RawValue{Type: bson.TypeInt32, Value: []byte{1, 0, 0, 0, 0}}},
		{"binary old subtype length", bson.RawValue{Type: bson.TypeBinary, Value: []byte{5, 0, 0, 0, 2, 0, 0, 0, 0, 7}}},
	}
	registry := mustFilterRegistry(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter := bson.D{{Key: "x", Value: tc.value}}
			if _, err := freezeFilter(registry, filter); !errors.Is(err, ErrValue) {
				t.Fatalf("authored raw = %v", err)
			}
			raw := bsoncore.BuildDocument(nil, bsoncore.AppendValueElement(nil, "x", bsoncore.Value{Type: bsoncore.Type(tc.value.Type), Data: tc.value.Value}))
			assertInvalidFilterEnvelope(t, raw)
			outer := bsoncore.BuildDocument(nil, bsoncore.AppendDocumentElement(nil, "outer", raw))
			assertInvalidFilterEnvelope(t, outer)
		})
	}
	assertInvalidFilterEnvelope(t, seed)
	// The original first key must not be normalized before rejection.
	if !bytes.Equal(bson.Raw(seed).Lookup("a").Value, array) {
		t.Fatal("raw input mutated")
	}
}

func assertInvalidFilterEnvelope(t *testing.T, raw []byte) {
	t.Helper()
	data, err := json.Marshal(findEnvelope{BSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	q := Find[author]{Filter: bson.D{{Key: "keep", Value: int64(9)}}}
	before := q.Filter
	if err := q.UnmarshalJSON(data); !errors.Is(err, ErrValue) || !reflect.DeepEqual(q.Filter, before) {
		t.Fatalf("malformed envelope accepted or changed destination: %v", err)
	}
}

func TestValidRawBSONCorpusRemainsLossless(t *testing.T) {
	decimal, err := bson.ParseDecimal128("123.450")
	if err != nil {
		t.Fatal(err)
	}
	values := []any{
		int32(1), int64(1), float64(1.25), true, bson.Null{},
		"a\x00b", bson.JavaScript("return x"), bson.Symbol("s"),
		bson.NewObjectID(), decimal, bson.DateTime(-1234), bson.Timestamp{T: 1, I: 2},
		bson.Regex{Pattern: "a.*", Options: "im"}, bson.DBPointer{DB: "db", Pointer: bson.NewObjectID()},
		bson.Binary{Subtype: 2, Data: []byte{0, 1, 255}}, bson.Binary{Subtype: 0x80, Data: []byte{1, 2}},
		bson.Undefined{}, bson.MinKey{}, bson.MaxKey{},
		bson.D{{Key: "duplicate", Value: int32(2)}, {Key: "duplicate", Value: int64(3)}},
		bson.A{int32(4), int64(5), bson.D{{Key: "child", Value: "value"}}},
		bson.CodeWithScope{Code: "return x", Scope: bson.D{{Key: "duplicate", Value: int32(2)}, {Key: "duplicate", Value: int64(3)}}},
	}
	registry := mustFilterRegistry(t)
	for i, value := range values {
		t.Run(bson.Type(mustRawValue(t, value).Type).String(), func(t *testing.T) {
			rawValue := mustRawValue(t, value)
			raw := bsoncore.BuildDocument(nil, bsoncore.AppendValueElement(nil, "value", bsoncore.Value{Type: bsoncore.Type(rawValue.Type), Data: rawValue.Value}))
			filter, err := decodeFilter(raw)
			if err != nil {
				t.Fatalf("corpus %d rejected: %v", i, err)
			}
			after, err := freezeFilter(registry, filter)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatalf("corpus %d changed: %x -> %x (%v)", i, raw, after, err)
			}
			for _, authored := range []any{rawValue, bson.Raw(raw)} {
				before, err := freezeFilter(registry, bson.D{{Key: "raw", Value: authored}})
				if err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(findEnvelope{BSON: before})
				if err != nil {
					t.Fatal(err)
				}
				var q Find[author]
				if err := q.UnmarshalJSON(data); err != nil {
					t.Fatal(err)
				}
				again, err := freezeFilter(registry, q.Filter)
				if err != nil || !bytes.Equal(before, again) {
					t.Fatalf("authored corpus %d changed: %v", i, err)
				}
			}
		})
	}
}

func mustRawValue(t *testing.T, value any) bson.RawValue {
	t.Helper()
	kind, raw, err := bson.MarshalValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return bson.RawValue{Type: kind, Value: raw}
}
