// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"encoding/json"
	"reflect"
	"testing"
)

type changeItem struct {
	ID   *string `json:"id"`
	Name string  `json:"name"`
}

func changeID(s string) *string { return &s }
func assertChanges(t *testing.T, changes *ChangeSet, want string) {
	t.Helper()
	encoded, err := changes.MarshalJSON()
	if err != nil || string(encoded) != want {
		t.Fatalf("changes = %s, %v; want %s", encoded, err, want)
	}
}
func TestCollectionChangesIdentityOrderingNullDuplicatesAndFrozenRemoval(t *testing.T) {
	previous := []changeItem{{changeID("b"), "old"}, {changeID("a"), "gone"}, {nil, "ignored"}}
	current := []changeItem{{changeID("c"), "first"}, {changeID("b"), "changed"}, {changeID("c"), "last"}, {nil, "ignored"}}
	changes, err := ComputeChanges(previous, current)
	if err != nil {
		t.Fatal(err)
	}
	previous[1].Name = "private"
	current[1].Name = "mutated"
	assertChanges(t, changes, `{"added":[{"id":"c","name":"last"}],"replaced":[{"id":"b","name":"changed"}],"removed":[{"id":"a","name":"gone"}]}`)
}
func TestCollectionChangesInitialAndReordering(t *testing.T) {
	a := changeItem{changeID("a"), "A"}
	b := changeItem{changeID("b"), "B"}
	changes, err := ComputeChanges[changeItem](nil, []changeItem{a, b})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, changes, `{"added":[{"id":"a","name":"A"},{"id":"b","name":"B"}],"replaced":[],"removed":[]}`)
	changes, err = ComputeChanges([]changeItem{a, b}, []changeItem{b, a})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, changes, `{"added":[],"replaced":[],"removed":[]}`)
	changes, err = ComputeChanges([]changeItem{a}, []changeItem{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, changes, `{"added":[],"replaced":[],"removed":[{"id":"a","name":"A"}]}`)
}
func TestCollectionChangesJSONSetAndUnsupportedValues(t *testing.T) {
	changes, err := ComputeChanges([]string{"a", "b"}, []string{"b", "c", "c"})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, changes, `{"added":["c","c"],"replaced":[],"removed":["a"]}`)
	if _, err := ComputeChanges[chan int](nil, []chan int{make(chan int)}); err == nil {
		t.Fatal("unencodable item accepted")
	}
}
func TestKnownChangesContinuityAndPrivacyFallback(t *testing.T) {
	shape, err := compileCollection(reflect.TypeFor[[]changeItem](), nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := shape.freeze([]changeItem{{changeID("a"), "masked"}, {changeID("b"), "old"}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := shape.freeze([]changeItem{{changeID("b"), "new"}, {changeID("c"), "added"}})
	if err != nil {
		t.Fatal(err)
	}
	old.hints = collectionHints{version: 1, generation: "g"}
	next.hints = collectionHints{version: 2, previous: 1, generation: "g", changes: []CollectionChange{{CollectionAdded, "c"}, {CollectionReplaced, "b"}, {CollectionRemoved, "a"}, {CollectionRemoved, "unknown"}}}
	want := `{"added":[{"id":"c","name":"added"}],"replaced":[{"id":"b","name":"new"}],"removed":[{"id":"a","name":"masked"}]}`
	assertChanges(t, shape.knownChanges(old, next), want)
	// Incomplete hints, missed predecessor and changed generation cannot hide changes.
	for _, hints := range []collectionHints{
		{version: 2, previous: 1, generation: "g", changes: []CollectionChange{{CollectionAdded, "c"}}},
		{version: 3, previous: 2, generation: "g", changes: []CollectionChange{}},
		{version: 2, previous: 1, generation: "new", changes: []CollectionChange{}},
		{version: 2, previous: 1, generation: "g", changes: []CollectionChange{{CollectionChangeKind(200), "b"}}},
	} {
		next.hints = hints
		assertChanges(t, shape.knownChanges(old, next), want)
	}
}
func TestCollectionIdentityIsCompiledFromMemberNotKeyTag(t *testing.T) {
	type keyed struct {
		Key  string `arc:"key"`
		Name string
	}
	shape, err := compileCollection(reflect.TypeFor[[]keyed](), nil)
	if err != nil || shape.identity != nil {
		t.Fatalf("key tag became collection identity: %v", err)
	}
	type alternate struct {
		Key  string
		Name string
	}
	explicit := &collectionIdentity{itemType: reflect.TypeFor[alternate](), get: func(v any) (any, error) { return v.(alternate).Key, nil }}
	shape, err = compileCollection(reflect.TypeFor[[]alternate](), explicit)
	if err != nil || shape.identity == nil {
		t.Fatal(err)
	}
	if _, err := compileCollection(reflect.TypeFor[[]keyed](), explicit); err == nil {
		t.Fatal("wrong item type accepted")
	}
}
func FuzzCollectionChanges(f *testing.F) {
	f.Add(`[ {"id":"a","name":"old"} ]`, `[ {"id":"b","name":"new"} ]`)
	f.Add(`[]`, `[]`)
	f.Fuzz(func(t *testing.T, before, after string) {
		if len(before)+len(after) > 65536 {
			t.Skip()
		}
		var old, next []changeItem
		if json.Unmarshal([]byte(before), &old) != nil || json.Unmarshal([]byte(after), &next) != nil {
			return
		}
		changes, err := ComputeChanges(old, next)
		if err != nil {
			return
		}
		encoded, err := changes.MarshalJSON()
		if err != nil || !json.Valid(encoded) {
			t.Fatalf("invalid change-set: %s, %v", encoded, err)
		}
		shape, err := compileCollection(reflect.TypeFor[[]changeItem](), nil)
		if err != nil {
			t.Fatal(err)
		}
		previous, err := shape.freeze(old)
		if err != nil {
			t.Fatal(err)
		}
		current, err := shape.freeze(next)
		if err != nil {
			t.Fatal(err)
		}
		// On the identity path, applying a computed delta reconstructs the keyed snapshot.
		if old == nil {
			return
		}
		items, _ := indexItems(previous, true)
		for _, group := range []struct {
			items  []any
			remove bool
		}{{changes.Removed, true}, {changes.Added, false}, {changes.Replaced, false}} {
			for _, raw := range group.items {
				var item changeItem
				if err := json.Unmarshal(raw.(json.RawMessage), &item); err != nil {
					t.Fatal(err)
				}
				key, err := identityKey(item.ID)
				if err != nil {
					t.Fatal(err)
				}
				if group.remove {
					delete(items, key)
				} else {
					items[key] = raw.(json.RawMessage)
				}
			}
		}
		want, _ := indexItems(current, true)
		if !reflect.DeepEqual(items, want) {
			t.Fatalf("delta does not reconstruct identity snapshot")
		}
	})
}
