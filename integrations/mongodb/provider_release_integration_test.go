//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/queries"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestLiveSyntheticReleaseReceivesWholeRawBSONBeforeDecodeAndInterception(t *testing.T) {
	f := liveProvider(t)
	collection := f.collection(t, f.a, "Release")
	// Ciphertext/lineage are synthetic boundary fixtures, NOT Chronicle layout.
	insertRows(t, collection, []any{
		bson.D{{Key: "_id", Value: int32(1)}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{0, 1, 2, 255}}}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}, {Key: "Lineage", Value: bson.D{{Key: "subject", Value: "synthetic-only"}}}},
		bson.D{{Key: "_id", Value: int32(2)}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{9, 8, 0, 7}}}, {Key: "Active", Value: true}, {Key: "OwnerID", Value: "reader"}, {Key: "Lineage", Value: bson.A{"opaque", int64(42)}}},
	})
	cursor, err := collection.Find(t.Context(), bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	var expected []bson.Raw
	if err := cursor.All(t.Context(), &expected); err != nil {
		t.Fatal(err)
	}
	if err := cursor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	binding, err := mongodb.NewCollection[snapshot.Author](f.client, mongodb.CollectionOptions{Database: f.base, Name: "Release", Ownership: mongodb.ChronicleOwned})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mongodb.NewRenderer(binding, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter}); !errors.Is(err, mongodb.ErrReleaseRequired) {
		t.Fatal("missing release accepted", err)
	}
	failure := errors.New("synthetic release failure")
	for _, mode := range []string{"valid", "error", "partial", "reordered", "identity"} {
		t.Run(mode, func(t *testing.T) {
			released, intercepted := false, 0
			app := registeredProvider[snapshot.Author](t, f, "Release", mongodb.ChronicleOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter, Release: func(_ context.Context, raw []bson.Raw) ([]snapshot.Author, error) {
				released = true
				if len(raw) != 2 || !bytes.Equal(raw[0], expected[0]) || !bytes.Equal(raw[1], expected[1]) || raw[0].Lookup("Name").Type != bson.TypeBinary || raw[1].Lookup("Lineage").Type != bson.TypeArray {
					t.Fatal("release did not receive full raw stored bytes before typed decoding")
				}
				data := []snapshot.Author{{ID: 1, Name: "released first", Active: true}, {ID: 2, Name: "released second", Active: true}}
				switch mode {
				case "error":
					return data, failure
				case "partial":
					return data[:1], nil
				case "reordered":
					slices.Reverse(data)
				case "identity":
					data[0].ID = 99
				}
				return data, nil
			}}, activeFilter(), queries.InterceptorFunc[snapshot.Author](func(_ context.Context, row snapshot.Author) (snapshot.Author, error) {
				if !released || !strings.HasPrefix(row.Name, "released") {
					t.Fatal("interception preceded release")
				}
				intercepted++
				return row, nil
			}), nil)
			// Sort only _id: protected Name is intentionally not a sink sort key.
			parameters := paged(0, 2)
			parameters.Sorting = queries.Sorting{}
			result, err := typedSnapshot[snapshot.Author](t, app, f.a, parameters)
			if !released {
				t.Fatal("release not called")
			}
			if mode == "valid" {
				data, present := result.Data()
				if err != nil || !result.IsSuccess() || !present || len(data) != 2 || data[0].Name != "released first" || intercepted != 2 {
					t.Fatal("release success", result.Details(), err)
				}
			} else {
				assertNoProviderPublication(t, result, err)
				if intercepted != 0 {
					t.Fatal("failed release reached interception")
				}
				if mode == "error" && !errors.Is(err, failure) {
					t.Fatal("lost release error", err)
				}
				if mode != "error" && !errors.Is(err, mongodb.ErrValue) {
					t.Fatal("lost release mismatch category", err)
				}
			}
		})
	}
	// Application-owned ordinary decoding must reject those protected bytes,
	// proving the successful callback path did not pre-decode the sink fields.
	ordinary := registeredProvider[snapshot.Author](t, f, "Release", mongodb.ApplicationOwned, mongodb.RendererOptions[snapshot.Author]{RowFilter: ownerFilter}, activeFilter(), nil, nil)
	parameters := paged(0, 2)
	parameters.Sorting = queries.Sorting{}
	result, err := typedSnapshot[snapshot.Author](t, ordinary, f.a, parameters)
	assertNoProviderPublication(t, result, err)
	if !errors.Is(err, mongodb.ErrValue) {
		t.Fatal(err)
	}
}
