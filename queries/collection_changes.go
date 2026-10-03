// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/cratis/arc.go/serialization"
)

type collectionIdentity struct {
	itemType reflect.Type
	get      func(any) (any, error)
}
type collectionShape struct {
	typ      reflect.Type
	identity *collectionIdentity
}
type frozenItem struct {
	key  string
	json json.RawMessage
}
type collectionSnapshot struct {
	items []frozenItem
	// bytes includes serialized identities/items and conservative indexing overhead.
	bytes int64
	hints collectionHints
}

func compileCollection(t reflect.Type, explicit *collectionIdentity) (*collectionShape, error) {
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		if explicit != nil {
			return nil, ErrResponseType
		}
		return nil, nil
	}
	item := t.Elem()
	shape := &collectionShape{typ: t, identity: explicit}
	if explicit != nil {
		if explicit.itemType != item {
			return nil, ErrResponseType
		}
		return shape, nil
	}
	base := item
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct {
		return shape, nil
	}
	for _, field := range reflect.VisibleFields(base) {
		if field.PkgPath != "" || !strings.EqualFold(field.Name, "id") {
			continue
		}
		index := append([]int{}, field.Index...)
		shape.identity = &collectionIdentity{itemType: item, get: func(value any) (any, error) {
			v := reflect.ValueOf(value)
			if nilValue(value) {
				return nil, nil
			}
			if v.Kind() == reflect.Pointer {
				v = v.Elem()
			}
			for _, i := range index {
				if v.Kind() == reflect.Pointer {
					if v.IsNil() {
						return nil, nil
					}
					v = v.Elem()
				}
				v = v.Field(i)
			}
			return v.Interface(), nil
		}}
		break
	}
	return shape, nil
}

func identityKey(value any) (string, error) {
	if nilValue(value) {
		return "", nil
	}
	encoded, err := serialization.Marshal(value)
	if err != nil {
		return "", err
	}
	if bytes.Equal(encoded, []byte("null")) {
		return "", nil
	}
	return string(encoded), nil
}

func (s *collectionShape) freeze(value any) (*collectionSnapshot, error) {
	if value == nil {
		return nil, nil
	}
	v := reflect.ValueOf(value)
	if v.Type() != s.typ {
		return nil, ErrResponseType
	}
	snapshot := &collectionSnapshot{items: make([]frozenItem, 0, v.Len())}
	for i := 0; i < v.Len(); i++ {
		item := v.Index(i).Interface()
		encoded, err := serialization.Marshal(item)
		if err != nil {
			return nil, err
		}
		key := string(encoded)
		if s.identity != nil {
			id, err := s.identity.get(item)
			if err != nil {
				return nil, err
			}
			key, err = identityKey(id)
			if err != nil {
				return nil, err
			}
		}
		snapshot.items = append(snapshot.items, frozenItem{key, encoded})
		snapshot.bytes += int64(len(key)) + int64(len(encoded)) + 128
	}
	return snapshot, nil
}

func indexItems(s *collectionSnapshot, identity bool) (map[string]json.RawMessage, []string) {
	items := map[string]json.RawMessage{}
	order := []string{}
	if s == nil {
		return items, order
	}
	for _, item := range s.items {
		if identity && item.key == "" {
			continue
		}
		if _, exists := items[item.key]; !exists {
			order = append(order, item.key)
		}
		items[item.key] = item.json // Last duplicate wins; first encounter fixes order.
	}
	return items, order
}

func (s *collectionShape) changes(previous, current *collectionSnapshot) *ChangeSet {
	changes := &ChangeSet{}
	if current == nil {
		return changes
	}
	if previous == nil {
		for _, item := range current.items {
			changes.Added = append(changes.Added, item.json)
		}
		return changes
	}
	identity := s.identity != nil
	old, oldOrder := indexItems(previous, identity)
	next, nextOrder := indexItems(current, identity)
	if !identity {
		// Reference JSON-set comparison preserves repeated additions/removals.
		for _, item := range current.items {
			if _, exists := old[item.key]; !exists {
				changes.Added = append(changes.Added, item.json)
			}
		}
		for _, item := range previous.items {
			if _, exists := next[item.key]; !exists {
				changes.Removed = append(changes.Removed, item.json)
			}
		}
		return changes
	}
	for _, key := range nextOrder {
		before, exists := old[key]
		if !exists {
			changes.Added = append(changes.Added, next[key])
		} else if !bytes.Equal(before, next[key]) {
			changes.Replaced = append(changes.Replaced, next[key])
		}
	}
	for _, key := range oldOrder {
		if _, exists := next[key]; !exists {
			changes.Removed = append(changes.Removed, old[key])
		}
	}
	return changes
}

// ComputeChanges freezes both snapshots through the Arc codec and computes item
// additions, replacements, and removals. A nil previous slice is the initial
// snapshot (all current items are added). ID/Id exported members select identity
// comparison; otherwise JSON-set comparison produces no replacements. Later
// duplicate IDs use the last value in first-encounter order; null IDs are skipped.
// Returned items are immutable serialized values, not aliases of either input.
func ComputeChanges[T any](previous, current []T) (*ChangeSet, error) {
	shape, err := compileCollection(reflect.TypeFor[[]T](), nil)
	if err != nil {
		return nil, err
	}
	next, err := shape.freeze(current)
	if err != nil {
		return nil, err
	}
	var old *collectionSnapshot
	if previous != nil {
		old, err = shape.freeze(previous)
		if err != nil {
			return nil, err
		}
	}
	return shape.changes(old, next), nil
}

// knownChanges uses hints only on delivered source continuity and only when their
// resolved result equals the independently computed intercepted diff. This is
// intentionally conservative: transformations and malformed hints cannot drop data.
func (s *collectionShape) knownChanges(previous, current *collectionSnapshot) *ChangeSet {
	ordinary := s.changes(previous, current)
	if previous == nil || s.identity == nil || current.hints.changes == nil ||
		current.hints.generation == "" || current.hints.generation != previous.hints.generation ||
		current.hints.previous == 0 || current.hints.previous != previous.hints.version ||
		current.hints.version <= current.hints.previous {
		return ordinary
	}
	old, _ := indexItems(previous, true)
	next, _ := indexItems(current, true)
	known := &ChangeSet{}
	for _, change := range current.hints.changes {
		key, err := identityKey(change.ID)
		if err != nil {
			return ordinary
		}
		switch change.Kind {
		case CollectionAdded:
			if item, ok := next[key]; ok {
				known.Added = append(known.Added, item)
			}
		case CollectionReplaced:
			if item, ok := next[key]; ok {
				known.Replaced = append(known.Replaced, item)
			}
		case CollectionRemoved:
			if item, ok := old[key]; ok {
				known.Removed = append(known.Removed, item)
			}
		default:
			return ordinary
		}
	}
	if equalChangeItems(known.Added, ordinary.Added) && equalChangeItems(known.Replaced, ordinary.Replaced) && equalChangeItems(known.Removed, ordinary.Removed) {
		return known
	}
	return ordinary
}
func equalChangeItems(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].(json.RawMessage), b[i].(json.RawMessage)) {
			return false
		}
	}
	return true
}
