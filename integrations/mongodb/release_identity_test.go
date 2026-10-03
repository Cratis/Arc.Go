// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type releasedUUIDRow struct {
	ID   concepts.UUID `json:"id" bson:"_id"`
	Name string        `json:"name" bson:"Name"`
}

func collisionRelease[T any](t *testing.T, documents []bson.Raw, reversed []T) {
	t.Helper()
	binding, err := NewCollection[T](&mongo.Client{}, CollectionOptions{Database: "Library", Name: "Rows", Ownership: ChronicleOwned})
	if err != nil {
		t.Fatal(err)
	}
	released := false
	renderer, err := NewRenderer(binding, RendererOptions[T]{RowFilter: func(context.Context, queries.QueryContext) (bson.D, error) { return bson.D{}, nil }, Release: func(context.Context, []bson.Raw) ([]T, error) {
		released = true
		return reversed, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	cursor := &recordingCursor{documents: documents}
	renderer.open = func(tenancy.ID) (snapshotCollection, error) {
		return &recordingCollection{total: 2, cursor: cursor}, nil
	}
	result, err := renderer.Execute(t.Context(), Find[T]{}, queries.QueryContext{})
	if !released || !errors.Is(err, ErrValue) || result.Data != nil || result.TotalItems != 0 || cursor.closes != 1 {
		t.Fatalf("ambiguous release: %v, %v, released %v, closes %d", result, err, released, cursor.closes)
	}
}

func TestReleaseRejectsDistinctBSONIDsCoercingToSameTypedIdentity(t *testing.T) {
	t.Run("integer and string", func(t *testing.T) {
		documents := []bson.Raw{
			rawDocument(t, bson.D{{Key: "_id", Value: int32(1)}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{1}}}}),
			rawDocument(t, bson.D{{Key: "_id", Value: "1"}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{2}}}}),
		}
		collisionRelease(t, documents, []author{{ID: 1, Name: "released second"}, {ID: 1, Name: "released first"}})
	})
	t.Run("UUID binary and string", func(t *testing.T) {
		id, err := concepts.ParseUUID("00112233-4455-6677-8899-aabbccddeeff")
		if err != nil {
			t.Fatal(err)
		}
		documents := []bson.Raw{
			rawDocument(t, bson.D{{Key: "_id", Value: bson.Binary{Subtype: 4, Data: []byte{0, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}}}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{1}}}}),
			rawDocument(t, bson.D{{Key: "_id", Value: "00112233-4455-6677-8899-aabbccddeeff"}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{2}}}}),
		}
		collisionRelease(t, documents, []releasedUUIDRow{{ID: id, Name: "released second"}, {ID: id, Name: "released first"}})
	})
}
