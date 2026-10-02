// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"encoding/json"

	"github.com/cratis/arc.go/internal/wire"
)

// ChangeSet carries items (not IDs) added, replaced and removed. It is only a wire
// value: this package does not compute differences or implement observable delivery.
type ChangeSet struct {
	// Added contains new items; nil encodes as []. Items are borrowed.
	Added []any
	// Replaced contains changed items with existing identities. Items are borrowed.
	Replaced []any
	// Removed contains the removed items, not merely their keys. Items are borrowed.
	Removed []any
}

// MarshalJSON always emits three arrays through the Arc model codec.
func (c ChangeSet) MarshalJSON() ([]byte, error) {
	added, err := wire.Payload(append([]any{}, c.Added...))
	if err != nil {
		return nil, err
	}
	replaced, err := wire.Payload(append([]any{}, c.Replaced...))
	if err != nil {
		return nil, err
	}
	removed, err := wire.Payload(append([]any{}, c.Removed...))
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Added    json.RawMessage `json:"added"`
		Replaced json.RawMessage `json:"replaced"`
		Removed  json.RawMessage `json:"removed"`
	}{added, replaced, removed})
}

func cloneChanges(c *ChangeSet) *ChangeSet {
	if c == nil {
		return nil
	}
	return &ChangeSet{append([]any{}, c.Added...), append([]any{}, c.Replaced...), append([]any{}, c.Removed...)}
}
