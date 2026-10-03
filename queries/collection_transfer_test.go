// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestDeliveredBaselineMovesOnlyAfterAcknowledgementAndReleasesReservations(t *testing.T) {
	shape, err := compileCollection(reflect.TypeFor[[]changeItem](), nil)
	if err != nil {
		t.Fatal(err)
	}
	retained := int64(0)
	transfer := collectionTransfer{shape: shape, mode: Delta, limit: 10000, reserve: func(n int64) (func(), error) { retained += n; return func() { retained -= n }, nil }}
	first := Success[any]([16]byte{}, []changeItem{{changeID("a"), "masked"}})
	result, commit, discard, err := transfer.prepare(first, collectionHints{})
	if err != nil {
		t.Fatal(err)
	}
	if transfer.delivered != nil || retained == 0 || result.Details().ChangeSet != nil {
		t.Fatal("queue admission committed")
	}
	commit()
	discard()
	if transfer.delivered == nil || retained == 0 {
		t.Fatal("successful delivery not committed")
	}
	// Callback data mutation cannot rewrite the frozen baseline/removal.
	items, _ := first.Data()
	items.([]changeItem)[0].Name = "private"
	next := Success[any]([16]byte{}, []changeItem{{changeID("b"), "new"}})
	result, commit, discard, err = transfer.prepare(next, collectionHints{})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := result.Data(); present {
		t.Fatal("delta carried full data")
	}
	assertChanges(t, result.Details().ChangeSet, `{"added":[{"id":"b","name":"new"}],"replaced":[],"removed":[{"id":"a","name":"masked"}]}`)
	// Simulated rejected queue/write: discard and a retry diff against a, never b.
	discard()
	commit()
	result, commit, discard, err = transfer.prepare(next, collectionHints{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, result.Details().ChangeSet, `{"added":[{"id":"b","name":"new"}],"replaced":[],"removed":[{"id":"a","name":"masked"}]}`)
	// Mutating exposed serialized changes cannot rewrite the successor baseline.
	for _, raw := range result.Details().ChangeSet.Added {
		raw.(json.RawMessage)[0] = 'x'
	}
	commit()
	discard()
	empty := Success[any]([16]byte{}, []changeItem{})
	removed, _, discard, err := transfer.prepare(empty, collectionHints{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, removed.Details().ChangeSet, `{"added":[],"replaced":[],"removed":[{"id":"b","name":"new"}]}`)
	discard()
	transfer.close()
	transfer.close()
	if retained != 0 {
		t.Fatalf("retained baseline bytes = %d", retained)
	}
}
func TestBaselineCapacityAndTransferProfiles(t *testing.T) {
	shape, err := compileCollection(reflect.TypeFor[[]changeItem](), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := Success[any]([16]byte{}, []changeItem{{changeID("a"), "A"}})
	for _, mode := range []TransferMode{Full, Legacy, Delta} {
		transfer := collectionTransfer{shape: shape, mode: mode, limit: 1000}
		first, commit, discard, err := transfer.prepare(result, collectionHints{})
		if err != nil {
			t.Fatal(err)
		}
		if (first.Details().ChangeSet != nil) != (mode == Legacy) {
			t.Fatalf("first changes mode %d", mode)
		}
		if _, present := first.Data(); !present {
			t.Fatal("first missing data")
		}
		commit()
		discard()
		second, commit, discard, err := transfer.prepare(result, collectionHints{})
		if err != nil {
			t.Fatal(err)
		}
		if (second.Details().ChangeSet != nil) != (mode != Full) {
			t.Fatalf("later changes mode %d", mode)
		}
		if _, present := second.Data(); present != (mode != Delta) {
			t.Fatalf("later data mode %d", mode)
		}
		commit()
		discard()
		transfer.close()
	}
	transfer := collectionTransfer{shape: shape, mode: Delta, limit: 1}
	if _, _, _, err := transfer.prepare(result, collectionHints{}); !errors.Is(err, ErrBaselineCapacity) {
		t.Fatalf("limit error = %v", err)
	}
	failure := errors.New("reservation rejected")
	transfer.limit = 1000
	transfer.reserve = func(int64) (func(), error) { return nil, failure }
	if _, _, _, err := transfer.prepare(result, collectionHints{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if transfer.delivered != nil {
		t.Fatal("failed reservation committed")
	}
}
