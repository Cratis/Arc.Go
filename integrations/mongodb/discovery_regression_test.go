// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var bsonHookCalls int

type bsonMarshalConcept int32

func (bsonMarshalConcept) ConceptValue() int32            { panic("must not execute") }
func (v bsonMarshalConcept) MarshalJSON() ([]byte, error) { return json.Marshal(int32(v)) }
func (v *bsonMarshalConcept) UnmarshalJSON(data []byte) error {
	var n int32
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = bsonMarshalConcept(n)
	return nil
}
func (v bsonMarshalConcept) MarshalText() ([]byte, error) { return amount(v).MarshalText() }
func (v *bsonMarshalConcept) UnmarshalText(data []byte) error {
	var n amount
	if err := n.UnmarshalText(data); err != nil {
		return err
	}
	*v = bsonMarshalConcept(n)
	return nil
}
func (bsonMarshalConcept) MarshalBSON() ([]byte, error) {
	bsonHookCalls++
	return nil, errors.New("secret BSON hook")
}

type bsonUnmarshalConcept int32

func (bsonUnmarshalConcept) ConceptValue() int32            { panic("must not execute") }
func (v bsonUnmarshalConcept) MarshalJSON() ([]byte, error) { return json.Marshal(int32(v)) }
func (v *bsonUnmarshalConcept) UnmarshalJSON(data []byte) error {
	var n int32
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = bsonUnmarshalConcept(n)
	return nil
}
func (v bsonUnmarshalConcept) MarshalText() ([]byte, error) { return amount(v).MarshalText() }
func (v *bsonUnmarshalConcept) UnmarshalText(data []byte) error {
	var n amount
	if err := n.UnmarshalText(data); err != nil {
		return err
	}
	*v = bsonUnmarshalConcept(n)
	return nil
}
func (*bsonUnmarshalConcept) UnmarshalBSON([]byte) error {
	bsonHookCalls++
	return errors.New("secret BSON hook")
}

type bsonValueMarshalConcept int32

func (bsonValueMarshalConcept) ConceptValue() int32            { panic("must not execute") }
func (v bsonValueMarshalConcept) MarshalJSON() ([]byte, error) { return json.Marshal(int32(v)) }
func (v *bsonValueMarshalConcept) UnmarshalJSON(data []byte) error {
	var n int32
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = bsonValueMarshalConcept(n)
	return nil
}
func (v bsonValueMarshalConcept) MarshalText() ([]byte, error) { return amount(v).MarshalText() }
func (v *bsonValueMarshalConcept) UnmarshalText(data []byte) error {
	var n amount
	if err := n.UnmarshalText(data); err != nil {
		return err
	}
	*v = bsonValueMarshalConcept(n)
	return nil
}
func (bsonValueMarshalConcept) MarshalBSONValue() (byte, []byte, error) {
	bsonHookCalls++
	return byte(bson.TypeNull), nil, errors.New("secret BSON hook")
}

type bsonValueUnmarshalConcept int32

func (bsonValueUnmarshalConcept) ConceptValue() int32            { panic("must not execute") }
func (v bsonValueUnmarshalConcept) MarshalJSON() ([]byte, error) { return json.Marshal(int32(v)) }
func (v *bsonValueUnmarshalConcept) UnmarshalJSON(data []byte) error {
	var n int32
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*v = bsonValueUnmarshalConcept(n)
	return nil
}
func (v bsonValueUnmarshalConcept) MarshalText() ([]byte, error) { return amount(v).MarshalText() }
func (v *bsonValueUnmarshalConcept) UnmarshalText(data []byte) error {
	var n amount
	if err := n.UnmarshalText(data); err != nil {
		return err
	}
	*v = bsonValueUnmarshalConcept(n)
	return nil
}
func (*bsonValueUnmarshalConcept) UnmarshalBSONValue(byte, []byte) error {
	bsonHookCalls++
	return errors.New("secret BSON hook")
}

type bsonDocumentSlice []int32

func (*bsonDocumentSlice) UnmarshalBSON([]byte) error {
	bsonHookCalls++
	return errors.New("secret BSON hook")
}

type bsonValueSlice []int32

func (*bsonValueSlice) MarshalBSONValue() (byte, []byte, error) {
	bsonHookCalls++
	return byte(bson.TypeNull), nil, errors.New("secret BSON hook")
}

type zeroerConcept int32

func (zeroerConcept) ConceptValue() int32            { panic("must not execute") }
func (v zeroerConcept) MarshalJSON() ([]byte, error) { return amount(v).MarshalJSON() }
func (v *zeroerConcept) UnmarshalJSON(data []byte) error {
	return (*amount)(v).UnmarshalJSON(data)
}
func (v zeroerConcept) MarshalText() ([]byte, error) { return amount(v).MarshalText() }
func (v *zeroerConcept) UnmarshalText(data []byte) error {
	return (*amount)(v).UnmarshalText(data)
}
func (zeroerConcept) IsZero() bool { bsonHookCalls++; return false }

type zeroerScalar int32

func (zeroerScalar) IsZero() bool { bsonHookCalls++; return false }

type zeroerSlice []int32

func (*zeroerSlice) IsZero() bool { bsonHookCalls++; return false }

type zeroerMap map[string]int32

func (zeroerMap) IsZero() bool { bsonHookCalls++; return false }

type zeroerArray [1]int32

func (zeroerArray) IsZero() bool { bsonHookCalls++; return false }

type zeroerModel struct {
	Field int32 `json:"field"`
}

func (*zeroerModel) IsZero() bool { bsonHookCalls++; return false }

func TestBSONZeroersRejectedBeforeAllDiscoveryFastPaths(t *testing.T) {
	bsonHookCalls = 0
	if _, recognized, err := concepts.Underlying(reflect.TypeFor[zeroerConcept]()); err != nil || !recognized {
		t.Fatalf("fixture not recognized as a concept: %v %v", recognized, err)
	}
	for _, typeOf := range []reflect.Type{
		reflect.TypeFor[zeroerConcept](), reflect.TypeFor[*zeroerConcept](),
		reflect.TypeFor[zeroerScalar](), reflect.TypeFor[zeroerSlice](), reflect.TypeFor[*zeroerSlice](),
		reflect.TypeFor[zeroerMap](), reflect.TypeFor[zeroerArray](),
		reflect.TypeFor[zeroerModel](), reflect.TypeFor[*zeroerModel](),
		reflect.TypeFor[valueBox[zeroerConcept]](), reflect.TypeFor[valueBox[zeroerSlice]](),
		reflect.TypeFor[valueBox[valueBox[zeroerModel]]](),
	} {
		t.Run(typeOf.String(), func(t *testing.T) {
			if registry, err := mongodb.NewRegistry(typeOf); registry != nil || !errors.Is(err, mongodb.ErrUnsupportedModel) {
				t.Fatalf("registry %v, error %v", registry, err)
			}
			if _, err := json.Marshal(mongodb.Find[valueBox[int32]]{Filter: bson.D{{Key: "x", Value: reflect.Zero(typeOf).Interface()}}}); !errors.Is(err, mongodb.ErrValue) {
				t.Fatalf("zeroer filter = %v", err)
			}
			if bsonHookCalls != 0 {
				t.Fatalf("discovery or encoding executed IsZero %d times", bsonHookCalls)
			}
		})
	}
	// Ordinary concepts still keep their required JSON/text conversions.
	if _, err := mongodb.NewRegistry(reflect.TypeFor[valueBox[amount]]()); err != nil {
		t.Fatal(err)
	}
}

func TestCachedSubtreeCannotBypassDepthLimit(t *testing.T) {
	p64 := reflect.TypeFor[int32]()
	for range 64 {
		p64 = reflect.PointerTo(p64)
	}
	p65 := reflect.PointerTo(p64)
	for _, roots := range [][]reflect.Type{{p64, p65}, {p65, p64}, {p64, reflect.SliceOf(p64)}, {reflect.SliceOf(p64), p64}} {
		if _, err := mongodb.NewRegistry(roots...); !errors.Is(err, mongodb.ErrUnsupportedModel) {
			t.Fatalf("cached depth accepted: %v", err)
		}
	}
	p63 := p64.Elem()
	// Shared subgraphs at the actual bound remain valid in either order.
	for _, roots := range [][]reflect.Type{{p63, p64, reflect.SliceOf(p63)}, {reflect.SliceOf(p63), p64, p63}} {
		if _, err := mongodb.NewRegistry(roots...); err != nil {
			t.Fatalf("valid shared graph rejected: %v", err)
		}
	}
}

func TestBSONHooksRejectedBeforeAllDiscoveryFastPaths(t *testing.T) {
	bsonHookCalls = 0
	for _, typeOf := range []reflect.Type{reflect.TypeFor[bsonMarshalConcept](), reflect.TypeFor[bsonUnmarshalConcept](), reflect.TypeFor[bsonValueMarshalConcept](), reflect.TypeFor[bsonValueUnmarshalConcept]()} {
		if _, recognized, err := concepts.Underlying(typeOf); err != nil || !recognized {
			t.Fatalf("fixture not recognized as a concept: %v %v", typeOf, err)
		}
	}
	for _, typeOf := range []reflect.Type{
		reflect.TypeFor[bsonMarshalConcept](), reflect.TypeFor[bsonUnmarshalConcept](), reflect.TypeFor[*bsonUnmarshalConcept](), reflect.TypeFor[bsonValueMarshalConcept](), reflect.TypeFor[bsonValueUnmarshalConcept](),
		reflect.TypeFor[bsonDocumentSlice](), reflect.TypeFor[*bsonDocumentSlice](), reflect.TypeFor[bsonValueSlice](),
		reflect.TypeFor[valueBox[bsonUnmarshalConcept]](), reflect.TypeFor[valueBox[bsonValueSlice]](),
	} {
		t.Run(typeOf.String(), func(t *testing.T) {
			// Decoder.Decode's root document hook precedes registry dispatch.
			if registry, err := mongodb.NewRegistry(typeOf); registry != nil || !errors.Is(err, mongodb.ErrUnsupportedModel) {
				t.Fatalf("registry %v, error %v", registry, err)
			}
			if bsonHookCalls != 0 {
				t.Fatal("discovery executed a BSON hook")
			}
		})
	}
}
