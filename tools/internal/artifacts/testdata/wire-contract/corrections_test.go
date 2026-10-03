// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package consumer

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func TestTemporalWire(t *testing.T) {
	for _, tc := range []struct {
		ticks int64
		wire  string
	}{
		{0, `"00:00:00"`}, {1, `"00:00:00.0000001"`}, {-1, `"-00:00:00.0000001"`},
		{math.MaxInt64, `"10675199.02:48:05.4775807"`}, {math.MinInt64, `"-10675199.02:48:05.4775808"`},
	} {
		data, err := serialization.Marshal(concepts.TimeSpan(tc.ticks))
		if err != nil || string(data) != tc.wire {
			t.Fatalf("ticks %d: %s, %v; want %s", tc.ticks, data, err, tc.wire)
		}
		var value concepts.TimeSpan
		if err := serialization.Unmarshal(data, &value); err != nil || int64(value) != tc.ticks {
			t.Fatalf("round trip %s: %v, %v", data, value, err)
		}
	}
	for _, invalid := range []string{`0`, `1`, `null`, `"PT1S"`} {
		var value concepts.TimeSpan
		if err := serialization.Unmarshal([]byte(invalid), &value); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	var value Save
	input := `{"span":"00:00:00.0000001","date":"2024-02-29","clock":"01:02:03.4000000","value":{}}`
	if err := serialization.Unmarshal([]byte(input), &value); err != nil {
		t.Fatal(err)
	}
	data, err := serialization.Marshal(value)
	if err != nil || string(data) != `{"clock":"01:02:03.4000000","date":"2024-02-29","span":"00:00:00.0000001","value":{}}` {
		t.Fatalf("temporal fields: %s, %v", data, err)
	}
}

func TestPointerPresenceWire(t *testing.T) {
	for _, tc := range []struct{ input, output string }{
		{`{}`, `{}`},
		{`{"pointer":null,"optional":null,"inner":null,"nested":null}`, `{}`},
		{`{"pointer":0,"optional":0,"inner":0,"nested":0}`, `{"inner":0,"nested":0,"optional":0,"pointer":0}`},
		{`{"pointer":7,"optional":7,"inner":7,"nested":7}`, `{"inner":7,"nested":7,"optional":7,"pointer":7}`},
	} {
		var value Presence
		if err := serialization.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Fatal(tc.input, err)
		}
		data, err := serialization.Marshal(value)
		if err != nil || string(data) != tc.output {
			t.Fatalf("%s: got %s, %v; want %s", tc.input, data, err, tc.output)
		}
	}
	// JSON null input clears the immediate pointer. Backend-created nonnil
	// wrappers can still publish null through their inner codec/pointer.
	var inner *int
	explicitNull := serialization.Null[int]()
	missing := serialization.Optional[int]{}
	zero, nonzero := serialization.Some(0), serialization.Some(7)
	missingPointer, nullPointer := &missing, &explicitNull
	var nilOptional *serialization.Optional[int]
	for _, tc := range []struct {
		value Presence
		wire  string
	}{
		{Presence{Optional: &explicitNull, Inner: &inner}, `{"inner":null,"optional":null}`},
		{Presence{Optional: &missing}, `{}`},
		{Presence{Optional: &zero}, `{"optional":0}`},
		{Presence{Optional: &nonzero}, `{"optional":7}`},
		{Presence{Nested: &missingPointer}, `{"nested":null}`},
		{Presence{Nested: &nullPointer}, `{"nested":null}`},
		{Presence{Nested: &nilOptional}, `{"nested":null}`},
	} {
		data, err := serialization.Marshal(tc.value)
		if err != nil || string(data) != tc.wire {
			t.Fatalf("backend presence: %s, %v; want %s", data, err, tc.wire)
		}
	}
}

func TestValidationStateWire(t *testing.T) {
	zero := State(0)
	for _, tc := range []struct {
		state any
		wire  string
	}{
		{State(math.MaxUint64), `18446744073709551615`},
		{SignedState(math.MinInt16), `-32768`},
		{zero, `0`}, {NullableState(&zero), `0`},
		{NullableState(nil), ""}, {NullableCodec{}, ""},
	} {
		data, err := serialization.Marshal(validation.Result{State: tc.state})
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		if string(object["state"]) != tc.wire {
			t.Fatalf("%T: %s; want state %s", tc.state, data, tc.wire)
		}
	}
}
