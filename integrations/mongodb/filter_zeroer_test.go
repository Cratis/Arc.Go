// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type filterZeroSlice []any

func (h filterZeroSlice) IsZero() bool {
	calls := h[1].(*int)
	*calls++
	h[0] = h // Initially acyclic; a driver field inspection would introduce a cycle.
	return false
}

type filterPointerZero struct{ calls *int }

func (h *filterPointerZero) IsZero() bool {
	*h.calls++
	return false
}

func TestFilterZeroersRejectBeforeEncodingWithoutHookExecution(t *testing.T) {
	calls := 0
	w, binding := testWatcher(t, WatcherOptions{}, newWatchTestCursor())
	t.Cleanup(func() { testWatchClose(t, w) })
	for name, value := range map[string]any{
		"named slice":                      filterZeroSlice{nil, &calls},
		"ordinary field without omitempty": struct{ Field filterZeroSlice }{filterZeroSlice{nil, &calls}},
		"pointer method set on value":      filterPointerZero{&calls},
		"pointer method set on pointer":    &filterPointerZero{&calls},
		"nil pointer":                      (*filterPointerZero)(nil),
		"nested named map":                 namedFilterMap{"field": &filterPointerZero{&calls}},
		"nested array":                     bson.A{struct{ Field filterZeroSlice }{filterZeroSlice{nil, &calls}}},
		"code scope":                       bson.CodeWithScope{Scope: struct{ Field filterZeroSlice }{filterZeroSlice{nil, &calls}}},
	} {
		t.Run(name, func(t *testing.T) {
			filter := bson.D{{Key: "x", Value: value}}
			if _, err := json.Marshal(Find[author]{Filter: filter}); !errors.Is(err, ErrValue) {
				t.Fatalf("zeroer marshal = %v", err)
			}
			if _, err := Observe(w, binding, Find[author]{Filter: filter}); !errors.Is(err, ErrValue) {
				t.Fatalf("zeroer observe = %v", err)
			}
			if calls != 0 {
				t.Fatalf("IsZero executed %d times", calls)
			}
		})
	}
}

func TestFilterKnownZeroerPrimitivesRemainSupported(t *testing.T) {
	for _, value := range []any{
		time.Time{}, &time.Time{}, bson.ObjectID{}, &bson.ObjectID{},
		bson.Decimal128{}, &bson.Decimal128{}, bson.Timestamp{}, &bson.Timestamp{},
		bson.Regex{}, &bson.Regex{}, bson.DBPointer{}, &bson.DBPointer{},
		bson.Binary{}, &bson.Binary{},
	} {
		filter := bson.D{{Key: "x", Value: struct{ Field any }{value}}}
		if _, err := json.Marshal(Find[author]{Filter: filter}); err != nil {
			t.Fatalf("known primitive %T: %v", value, err)
		}
	}
}
