// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type filterHook struct{ called *bool }

func (h filterHook) MarshalBSONValue() (byte, []byte, error) {
	*h.called = true
	return byte(bson.TypeNull), nil, nil
}

type filterJSONHook struct{ called *bool }

func (h filterJSONHook) MarshalJSON() ([]byte, error) {
	*h.called = true
	return []byte(`null`), nil
}

type namedFilterSlice []any
type namedFilterMap map[string]any
type filterNode struct{ Next *filterNode }
type recursiveFilterPointer *recursiveFilterPointer

func TestFilterGraphRejectsRecursivePointerTypes(t *testing.T) {
	var self recursiveFilterPointer
	self = &self
	w, binding := testWatcher(t, WatcherOptions{}, newWatchTestCursor())
	t.Cleanup(func() { testWatchClose(t, w) })
	for name, value := range map[string]recursiveFilterPointer{"nil": nil, "self": self} {
		t.Run(name, func(t *testing.T) {
			filter := bson.D{{Key: "x", Value: value}}
			if _, err := json.Marshal(Find[author]{Filter: filter}); !errors.Is(err, ErrValue) {
				t.Fatalf("recursive pointer marshal = %v", err)
			}
			if _, err := Observe(w, binding, Find[author]{Filter: filter}); !errors.Is(err, ErrValue) {
				t.Fatalf("recursive pointer observe = %v", err)
			}
		})
	}
}

func TestFilterGraphRejectsCyclesBeforeEncoding(t *testing.T) {
	d := bson.D{{Key: "self"}}
	d[0].Value = d
	a := bson.A{nil}
	a[0] = a
	m := namedFilterMap{}
	m["self"] = m
	s := namedFilterSlice{nil}
	s[0] = &s
	n := &filterNode{}
	n.Next = n
	var inter any
	inter = &inter
	for name, value := range map[string]any{"document": d, "array": a, "map": m, "named slice pointer": s, "pointer": n, "interface": inter, "scope": bson.CodeWithScope{Scope: d}} {
		t.Run(name, func(t *testing.T) {
			_, err := json.Marshal(Find[author]{Filter: bson.D{{Key: "x", Value: value}}})
			if !errors.Is(err, ErrValue) {
				t.Fatalf("cycle error = %v", err)
			}
		})
	}
}

func TestFilterGraphRejectsDepthAndOpaqueHooks(t *testing.T) {
	var value any = int32(1)
	for range maxFilterDepth + 1 {
		value = bson.A{value}
	}
	_, err := json.Marshal(Find[author]{Filter: bson.D{{Key: "x", Value: value}}})
	if !errors.Is(err, ErrValue) {
		t.Fatalf("deep graph = %v", err)
	}
	for _, wrap := range []func(any) any{
		func(v any) any { return v },
		func(v any) any { return bson.D{{Key: "nested", Value: v}} },
		func(v any) any { return namedFilterMap{"nested": v} },
		func(v any) any { return bson.CodeWithScope{Scope: bson.A{v}} },
	} {
		for _, hook := range []func(*bool) any{
			func(c *bool) any { return filterHook{c} },
			func(c *bool) any { return &filterHook{c} },
			func(c *bool) any { return filterJSONHook{c} },
		} {
			called := false
			_, err := json.Marshal(Find[author]{Filter: bson.D{{Key: "x", Value: wrap(hook(&called))}}})
			if !errors.Is(err, ErrValue) || called {
				t.Fatalf("hook = %v, executed = %v", err, called)
			}
		}
	}
}

func TestFilterByteBudgetPrecedesDriverBufferGrowth(t *testing.T) {
	registry, err := NewRegistry(reflect.TypeFor[author]())
	if err != nil {
		t.Fatal(err)
	}
	filter := bson.D{{Key: "x", Value: strings.Repeat("a", 1<<20)}}
	if _, err := freezeFilterBounded(registry, filter, 64); !errors.Is(err, ErrLimit) {
		t.Fatalf("byte limit = %v", err)
	}
	budget := &filterBudget{remaining: 64}
	writer := &filterWriter{nil, budget} // A failed admission must not call the underlying writer.
	if err := writer.WriteString(strings.Repeat("x", 65)); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if budget.remaining != 64 {
		t.Fatal("failed admission consumed budget")
	}
	filter[0].Value = strings.Repeat("a", maxFilterBytes+1)
	if _, err := json.Marshal(Find[author]{Filter: filter}); !errors.Is(err, ErrLimit) {
		t.Fatalf("opaque limit bypass = %v", err)
	}
}

func TestFilterEnvelopeBoundsBeforeDecode(t *testing.T) {
	q := Find[author]{Filter: bson.D{{Key: "keep", Value: int32(1)}}}
	before := q.Filter
	if err := q.UnmarshalJSON([]byte(strings.Repeat(" ", (maxFilterBytes*4/3)+2048))); !errors.Is(err, ErrLimit) || !reflect.DeepEqual(q.Filter, before) {
		t.Fatalf("large envelope = %v", err)
	}
	var raw bson.Raw = []byte{5, 0, 0, 0, 0}
	for range maxFilterDepth + 1 {
		data, err := bson.Marshal(bson.D{{Key: "nested", Value: raw}})
		if err != nil {
			t.Fatal(err)
		}
		raw = data
	}
	if _, err := decodeFilter(raw); !errors.Is(err, ErrValue) {
		t.Fatalf("deep BSON = %v", err)
	}
	if _, err := freezeFilterBounded(mustFilterRegistry(t), bson.D{{Key: "raw", Value: raw}}, maxFilterBytes); !errors.Is(err, ErrValue) {
		t.Fatalf("authored deep BSON = %v", err)
	}
}

func mustFilterRegistry(t *testing.T) *bson.Registry {
	t.Helper()
	r, err := NewRegistry(reflect.TypeFor[author]())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
