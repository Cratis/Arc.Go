//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cratis/arc.go/integrations/mongodb"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type providerProfile struct {
	ID        concepts.UUID     `json:"id" bson:"_id"`
	Name      string            `json:"name" bson:"Name"`
	Amount    amount            `json:"amount" bson:"Amount"`
	Optional  *string           `json:"optional" bson:"Optional"`
	Values    []string          `json:"values" bson:"Values"`
	Date      concepts.DateOnly `json:"date" bson:"Date"`
	Time      concepts.TimeOnly `json:"time" bson:"Time"`
	Timestamp time.Time         `json:"timestamp" bson:"Timestamp"`
	Span      concepts.TimeSpan `json:"span" bson:"Span"`
}

func TestLiveUUIDConceptNullAndTemporalProfileRoundTripAndAtomicDecodeFailure(t *testing.T) {
	f := liveProvider(t)
	var value providerProfile
	if err := json.Unmarshal([]byte(`{"id":"00112233-4455-6677-8899-aabbccddeeff","name":"profile","amount":17,"optional":null,"values":[],"date":"2024-02-29","time":"12:34:56.7890000","timestamp":"1969-12-31T23:59:59.123Z","span":"-1.02:03:04.0000005"}`), &value); err != nil {
		t.Fatal(err)
	}
	registry, err := mongodb.NewRegistry(reflect.TypeFor[providerProfile]())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := encode(registry, value)
	if err != nil {
		t.Fatal(err)
	}
	collection := f.collection(t, f.a, "Profile")
	if _, err := collection.InsertOne(t.Context(), bson.Raw(wire)); err != nil {
		t.Fatal(err)
	}
	var stored bson.Raw
	if err := collection.FindOne(t.Context(), bson.D{}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	subtype, uuid := stored.Lookup("_id").Binary()
	if subtype != 4 || !reflect.DeepEqual(uuid, []byte{0, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}) || stored.Lookup("Amount").Type != bson.TypeInt32 || stored.Lookup("Date").DateTime() != time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC).UnixMilli() || stored.Lookup("Time").DateTime() != 45296789 || stored.Lookup("Timestamp").DateTime() != -877 || stored.Lookup("Optional").Type != bson.TypeNull {
		t.Fatal("stored BSON profile", stored)
	}
	app := registeredProvider[providerProfile](t, f, "Profile", mongodb.ApplicationOwned, mongodb.RendererOptions[providerProfile]{RowFilter: func(_ context.Context, _ queries.QueryContext) (bson.D, error) { return bson.D{}, nil }}, nil, nil, nil)
	parameters := paged(0, 2)
	parameters.Sorting = queries.Sorting{}
	result, err := typedSnapshot[providerProfile](t, app, f.a, parameters)
	data, present := result.Data()
	if err != nil || !result.IsSuccess() || !present || len(data) != 1 || !reflect.DeepEqual(data[0], value) || data[0].Values == nil {
		t.Fatalf("round trip %+v want %+v, %v", data, value, err)
	}
	// A valid earlier row followed by an invalid stored scalar must not publish
	// a partial slice or its authorized total. The wire remains safe HTTP 500.
	if _, err := collection.InsertOne(t.Context(), bson.D{{Key: "_id", Value: "ffeeddcc-bbaa-9988-7766-554433221100"}, {Key: "Name", Value: "invalid stored"}, {Key: "Amount", Value: "not-a-number"}}); err != nil {
		t.Fatal(err)
	}
	result, err = typedSnapshot[providerProfile](t, app, f.a, parameters)
	assertNoProviderPublication(t, result, err)
	if !errors.Is(err, mongodb.ErrValue) {
		t.Fatal("invalid stored scalar category", err)
	}
	response := providerHTTP(t, app, f.a, "/rows?page=0&pageSize=2", true)
	if response.Code != 500 {
		t.Fatal("decode failure was HTTP success", response.Code, response.Body.String())
	}
	assertProviderEnvelope(t, response, false)
}
