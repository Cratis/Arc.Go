// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import "reflect"

// CollectionChangeKind describes an item-level source change, not a subscription revision.
type CollectionChangeKind uint8

const (
	// CollectionAdded identifies a new item.
	CollectionAdded CollectionChangeKind = iota
	// CollectionReplaced identifies updated content under an existing identity.
	CollectionReplaced
	// CollectionRemoved identifies an item no longer in the snapshot.
	CollectionRemoved
)

// CollectionChange names an identity in the current or previously delivered snapshot.
// Unknown identities are ignored. ID is borrowed and must be JSON-representable.
type CollectionChange struct {
	Kind CollectionChangeKind
	ID   any
}

// ObservedCollection carries a complete snapshot and optional source change hints.
// Items and Changes are borrowed immutable values, subject to the source's cloning
// contract. Emit this value, not a pointer wrapper; pointer declarations fail with
// ErrResponseType. The query pipeline renders Items as []T, not this metadata wrapper.
// Versions are source-local continuity hints, never wire subscription revisions.
// Zero versions and an empty Generation disable continuity hints. Hints are always
// verified against intercepted snapshots; they cannot hide independently found changes.
type ObservedCollection[T any] struct {
	Items           []T
	Changes         []CollectionChange
	Version         uint64
	PreviousVersion uint64
	Generation      string
}

type observedCollection interface {
	collectionItems() any
	collectionType() reflect.Type
	collectionHints() collectionHints
}
type collectionHints struct {
	changes           []CollectionChange
	version, previous uint64
	generation        string
}

func (c ObservedCollection[T]) collectionItems() any       { return c.Items }
func (ObservedCollection[T]) collectionType() reflect.Type { return reflect.TypeFor[[]T]() }
func (c ObservedCollection[T]) collectionHints() collectionHints {
	return collectionHints{c.Changes, c.Version, c.PreviousVersion, c.Generation}
}
