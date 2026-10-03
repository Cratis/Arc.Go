// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type inlineFilterCycle struct {
	Next *inlineFilterCycle `bson:",inline"`
}

type inlineFilterLeaf struct {
	Value int32 `bson:"value"`
}

type ordinaryInlineFilter struct {
	Leaf inlineFilterLeaf `bson:",inline"`
}

func TestFilterInlineFieldsRejectBeforeEncoding(t *testing.T) {
	w, binding := testWatcher(t, WatcherOptions{}, newWatchTestCursor())
	t.Cleanup(func() { testWatchClose(t, w) })
	for name, value := range map[string]any{
		"acyclic value with nil recursive inline field": inlineFilterCycle{},
		"acyclic pointer":            &inlineFilterCycle{},
		"nil pointer":                (*inlineFilterCycle)(nil),
		"nonrecursive inline struct": ordinaryInlineFilter{inlineFilterLeaf{1}},
		"nonrecursive nil inline pointer": struct {
			Leaf *inlineFilterLeaf `bson:",inline,omitempty"`
		}{},
		"nil inline map": struct {
			Fields bson.M `bson:",inline"`
		}{},
		"first-token inline": struct {
			Next *inlineFilterCycle `bson:"inline"`
		}{},
		"named inline": struct {
			Next *inlineFilterCycle `bson:"next,inline"`
		}{},
		"dash with inline is not skipped": struct {
			Next *inlineFilterCycle `bson:"-,inline"`
		}{},
		"legacy bare tag": reflect.Zero(reflect.StructOf([]reflect.StructField{{
			Name: "Next", Type: reflect.TypeFor[*inlineFilterCycle](), Tag: reflect.StructTag(",inline"),
		}})).Interface(),
		"nested nil pointer": struct{ Child *inlineFilterCycle }{},
		"nil parent pointer": (*struct{ Child inlineFilterCycle })(nil),
		"nil slice":          []inlineFilterCycle(nil),
		"empty slice":        []inlineFilterCycle{},
		"zero-length array":  [0]inlineFilterCycle{},
		"nil map":            map[string]inlineFilterCycle(nil),
		"empty map":          map[string]inlineFilterCycle{},
		"nested empty container": struct {
			Children []map[string]*inlineFilterCycle
		}{},
		"dynamic document": bson.D{{Key: "child", Value: inlineFilterCycle{}}},
		"dynamic array":    bson.A{inlineFilterCycle{}},
		"dynamic map":      bson.M{"child": inlineFilterCycle{}},
		"code scope":       bson.CodeWithScope{Scope: inlineFilterCycle{}},
	} {
		t.Run(name, func(t *testing.T) {
			filter := bson.D{{Key: "x", Value: value}}
			// Never pass these unsafe types directly to the driver, even if a
			// future regression removes the type guard. Fail before encoding.
			if err := guardFilter(filter, maxFilterBytes); !errors.Is(err, ErrValue) {
				t.Fatalf("inline preflight = %v", err)
			}
			if data, err := (Find[author]{Filter: filter}).MarshalJSON(); !errors.Is(err, ErrValue) || data != nil {
				t.Fatalf("inline marshal = %v, data present = %v", err, data != nil)
			}
			if source, err := Observe(w, binding, Find[author]{Filter: filter}); !errors.Is(err, ErrValue) || source != nil {
				t.Fatalf("inline observe = %v, source present = %v", err, source != nil)
			}
		})
	}
}

func TestFilterOrdinaryStructsRemainSupported(t *testing.T) {
	w, binding := testWatcher(t, WatcherOptions{}, newWatchTestCursor())
	t.Cleanup(func() { testWatchClose(t, w) })
	for name, value := range map[string]any{
		"ordinary recursive type with nil field": filterNode{},
		"nil ordinary pointer":                   (*filterNode)(nil),
		"ordinary nested struct":                 struct{ Leaf inlineFilterLeaf }{inlineFilterLeaf{7}},
		"nil ordinary slice":                     []inlineFilterLeaf(nil),
		"empty ordinary map":                     map[string]inlineFilterLeaf{},
		"JSON inline is not BSON inline": struct {
			Leaf inlineFilterLeaf `json:",inline"`
		}{inlineFilterLeaf{7}},
		"inline substring is not option": struct {
			Leaf inlineFilterLeaf `bson:"leaf,notinline"`
		}{inlineFilterLeaf{7}},
		"space-prefixed inline is not option": struct {
			Leaf inlineFilterLeaf `bson:"leaf, inline"`
		}{inlineFilterLeaf{7}},
		"unexported inline field is ignored": struct {
			leaf  inlineFilterLeaf `bson:",inline"`
			Value int32            `bson:"value"`
		}{Value: 7},
		"exact dash is skipped": struct {
			Leaf  inlineFilterLeaf `bson:"-"`
			Value int32            `bson:"value"`
		}{Value: 7},
		"native BSON": bson.D{{Key: "integer", Value: int64(7)}, {Key: "binary", Value: bson.Binary{Subtype: 4, Data: make([]byte, 16)}}},
	} {
		t.Run(name, func(t *testing.T) {
			filter := bson.D{{Key: "x", Value: value}}
			data, err := (Find[author]{Filter: filter}).MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			var detached Find[author]
			if err := json.Unmarshal(data, &detached); err != nil {
				t.Fatal(err)
			}
			want, err := bson.Marshal(filter)
			if err != nil {
				t.Fatal(err)
			}
			got, err := bson.Marshal(detached.Filter)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("ordinary BSON changed: %v", err)
			}
			if source, err := Observe(w, binding, Find[author]{Filter: filter}); err != nil || source == nil {
				t.Fatalf("ordinary observe = %v, source present = %v", err, source != nil)
			}
		})
	}
}
