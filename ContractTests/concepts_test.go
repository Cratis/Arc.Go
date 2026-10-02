// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"encoding"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/serialization"
	fconcepts "github.com/cratis/fundamentals.go/concepts"
)

type authorID concepts.UUID

func (id authorID) ConceptValue() concepts.UUID  { panic("discovery must not execute ConceptValue") }
func (id authorID) MarshalText() ([]byte, error) { return concepts.UUID(id).MarshalText() }
func (id authorID) MarshalJSON() ([]byte, error) { return concepts.UUID(id).MarshalJSON() }
func (id *authorID) UnmarshalText(data []byte) error {
	var value concepts.UUID
	if err := value.UnmarshalText(data); err != nil {
		return err
	}
	*id = authorID(value)
	return nil
}
func (id *authorID) UnmarshalJSON(data []byte) error {
	var value concepts.UUID
	if err := value.UnmarshalJSON(data); err != nil {
		return err
	}
	*id = authorID(value)
	return nil
}

type title string

func (v title) ConceptValue() string             { panic("discovery must not execute ConceptValue") }
func (v title) MarshalText() ([]byte, error)     { return []byte(v), nil }
func (v title) MarshalJSON() ([]byte, error)     { return json.Marshal(string(v)) }
func (v *title) UnmarshalText(data []byte) error { *v = title(data); return nil }
func (v *title) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*v = title(value)
	return nil
}

type count int32

func (v count) ConceptValue() int32          { panic("discovery must not execute ConceptValue") }
func (v count) MarshalText() ([]byte, error) { return []byte(strconv.FormatInt(int64(v), 10)), nil }
func (v count) MarshalJSON() ([]byte, error) { return json.Marshal(int32(v)) }
func (v *count) UnmarshalText(data []byte) error {
	value, err := strconv.ParseInt(string(data), 10, 32)
	if err != nil {
		return err
	}
	*v = count(value)
	return nil
}
func (v *count) UnmarshalJSON(data []byte) error {
	var value int32
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*v = count(value)
	return nil
}

var (
	_ concepts.Concept[concepts.UUID]  = authorID{}
	_ fconcepts.Concept[concepts.UUID] = authorID{}
	_ concepts.Concept[string]         = title("")
	_ concepts.Concept[int32]          = count(0)
	_ encoding.TextUnmarshaler         = (*authorID)(nil)
)

type conceptModel struct {
	Author  authorID         `json:"author"`
	Title   title            `json:"title"`
	Count   count            `json:"count"`
	Pointer *authorID        `json:"pointer"`
	IDs     []authorID       `json:"ids"`
	Lookup  map[string]title `json:"lookup"`
}

func TestConceptFieldsKeepTheirOwnCodecs(t *testing.T) {
	var id authorID
	if err := id.UnmarshalText([]byte("00112233-4455-6677-8899-aabbccddeeff")); err != nil {
		t.Fatal(err)
	}
	model := conceptModel{Author: id, Title: "Ada", Count: 42, Pointer: &id, IDs: []authorID{id}, Lookup: map[string]title{"book": "Arc"}}
	if err := serialization.ValidateType(reflect.TypeFor[conceptModel]()); err != nil {
		t.Fatal(err)
	}
	data, err := serialization.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"author":"00112233-4455-6677-8899-aabbccddeeff","count":42,"ids":["00112233-4455-6677-8899-aabbccddeeff"],"lookup":{"book":"Arc"},"pointer":"00112233-4455-6677-8899-aabbccddeeff","title":"Ada"}`
	if string(data) != want {
		t.Fatalf("JSON = %s, want %s", data, want)
	}
	var got conceptModel
	if err := serialization.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, model) {
		t.Fatalf("round trip = %#v, want %#v", got, model)
	}

	// Check actual encoded fields only in contract tests, never on Arc's hot path.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for name, typ := range map[string]reflect.Type{
		"author": reflect.TypeFor[authorID](), "title": reflect.TypeFor[title](),
		"count": reflect.TypeFor[count](), "pointer": reflect.TypeFor[*authorID](),
	} {
		r, ok, err := fconcepts.Underlying(typ)
		if err != nil || !ok {
			t.Fatalf("%s recognition = %v, %v", name, ok, err)
		}
		if err := fconcepts.CheckJSON(r, fields[name]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

type markerOnly string

func (markerOnly) ConceptValue() string { panic("must not execute invalid marker") }

type nestedConcept string

func (nestedConcept) ConceptValue() title { panic("must not execute nested marker") }

func TestInvalidConceptsFailDuringPlanning(t *testing.T) {
	cases := []struct {
		name   string
		typ    reflect.Type
		reason fconcepts.InvalidReason
	}{
		{"marker only", reflect.TypeFor[markerOnly](), fconcepts.ReasonMissingCodec},
		{"nested", reflect.TypeFor[nestedConcept](), fconcepts.ReasonNestedConcept},
		{"pointer", reflect.TypeFor[*markerOnly](), fconcepts.ReasonMissingCodec},
		{"omitted field", reflect.TypeFor[struct{ Value *markerOnly }](), fconcepts.ReasonMissingCodec},
		{"empty slice", reflect.TypeFor[[]markerOnly](), fconcepts.ReasonMissingCodec},
		{"empty map", reflect.TypeFor[map[string]markerOnly](), fconcepts.ReasonMissingCodec},
		{"map key", reflect.TypeFor[map[markerOnly]string](), fconcepts.ReasonMissingCodec},
		{"optional", reflect.TypeFor[serialization.Optional[markerOnly]](), fconcepts.ReasonMissingCodec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertInvalid := func(err error) {
				t.Helper()
				var typeErr *fconcepts.TypeError
				if !errors.Is(err, fconcepts.ErrInvalidConcept) || !errors.As(err, &typeErr) || typeErr.Reason != tc.reason {
					t.Fatalf("error = %v, want inspectable %s", err, tc.reason)
				}
			}
			assertInvalid(serialization.ValidateType(tc.typ))
			value := reflect.New(tc.typ)
			_, err := serialization.Marshal(value.Elem().Interface())
			assertInvalid(err)
			assertInvalid(serialization.Unmarshal([]byte("null"), value.Interface()))
		})
	}
}

func TestConceptPlanningIsConcurrentAndMetadataOnly(t *testing.T) {
	// A distinct type exercises initial plan publication rather than only cache hits.
	type concurrentModel struct{ Value conceptModel }
	if reflect.TypeFor[concepts.Concept[string]]() != reflect.TypeFor[fconcepts.Concept[string]]() {
		t.Fatal("Arc Concept alias lost shared type identity")
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if err := serialization.ValidateType(reflect.TypeFor[concurrentModel]()); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
}

func TestConceptPointerNullAndAtomicBinding(t *testing.T) {
	var ptr *authorID
	if err := serialization.Unmarshal([]byte("null"), &ptr); err != nil || ptr != nil {
		t.Fatalf("null pointer = %v, %v", ptr, err)
	}
	model := conceptModel{Title: "original", Count: 42}
	if err := serialization.Unmarshal([]byte(`{"title":"changed","author":"invalid"}`), &model); err == nil {
		t.Fatal("invalid UUID accepted")
	}
	if model.Title != "original" || model.Count != 42 {
		t.Fatal("failed binding mutated target")
	}
}
