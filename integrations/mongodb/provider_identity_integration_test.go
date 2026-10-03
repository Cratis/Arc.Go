//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type providerUUIDIdentity struct {
	ID   concepts.UUID `json:"id" bson:"_id"`
	Name string        `json:"name" bson:"Name"`
}

func liveIdentityCollision[T any](t *testing.T, f *providerFixture, name string, ids []any, reversed []T) {
	t.Helper()
	rows := make([]any, len(ids))
	for i, id := range ids {
		rows[i] = bson.D{{Key: "_id", Value: id}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{byte(i)}}}, {Key: "OwnerID", Value: "reader"}}
	}
	insertRows(t, f.collection(t, f.a, name), rows)
	released, intercepted := false, false
	app := registeredProvider[T](t, f, name, mongodb.ChronicleOwned, mongodb.RendererOptions[T]{RowFilter: ownerFilter, Release: func(_ context.Context, raw []bson.Raw) ([]T, error) {
		released = true
		if len(raw) != 2 || raw[0].Lookup("Name").Type != bson.TypeBinary || raw[1].Lookup("Name").Type != bson.TypeBinary {
			t.Fatal("identity check pre-decoded protected fields")
		}
		return reversed, nil
	}}, nil, queries.InterceptorFunc[T](func(_ context.Context, row T) (T, error) { intercepted = true; return row, nil }), nil)
	parameters := paged(0, 2)
	parameters.Sorting = queries.Sorting{}
	result, err := typedSnapshot[T](t, app, f.a, parameters)
	assertNoProviderPublication(t, result, err)
	if !errors.Is(err, mongodb.ErrValue) || !released || intercepted {
		t.Fatal("ambiguous correspondence published", result.Details(), err, released, intercepted)
	}
}

func TestLiveReleaseRejectsDistinctStoredIDsWithDuplicateDecodedIdentities(t *testing.T) {
	f := liveProvider(t)
	t.Run("integer and string", func(t *testing.T) {
		liveIdentityCollision(t, f, "IntegerIdentity", []any{int32(1), "1"}, []snapshot.Author{{ID: 1, Name: "released second"}, {ID: 1, Name: "released first"}})
	})
	t.Run("UUID binary and string", func(t *testing.T) {
		id, err := concepts.ParseUUID("00112233-4455-6677-8899-aabbccddeeff")
		if err != nil {
			t.Fatal(err)
		}
		liveIdentityCollision(t, f, "UUIDIdentity", []any{bson.Binary{Subtype: 4, Data: []byte{0, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}}, "00112233-4455-6677-8899-aabbccddeeff"}, []providerUUIDIdentity{{ID: id, Name: "released second"}, {ID: id, Name: "released first"}})
	})
}
