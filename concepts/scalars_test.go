// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package concepts_test

import (
	"encoding"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/cratis/arc.go/concepts"
)

func TestUUIDUsesRFCOrderAndCanonicalText(t *testing.T) {
	id, err := concepts.ParseUUID("00112233-4455-4677-8899-AABBCCDDEEFF")
	if err != nil {
		t.Fatal(err)
	}
	if id[0] != 0x00 || id[1] != 0x11 || id[4] != 0x44 || id[6] != 0x46 || id.String() != "00112233-4455-4677-8899-aabbccddeeff" {
		t.Fatalf("UUID = %x / %s", [16]byte(id), id)
	}
	if id.IsZero() || !(concepts.UUID{}).IsZero() {
		t.Fatal("zero UUID semantics")
	}
	generated, err := concepts.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if generated[6]>>4 != 4 || generated[8]>>6 != 2 {
		t.Fatalf("UUID version/variant = %x", [16]byte(generated))
	}
	for _, text := range []string{"", "bad", "00112233445546778899aabbccddeeff", "00112233-4455-4677-8899-aabbccddee-g", "00112233-4455-4677-8899-aabbccddeef-"} {
		if _, err := concepts.ParseUUID(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestTemporalBoundaries(t *testing.T) {
	leap, err := concepts.NewDateOnly(2024, time.February, 29)
	if err != nil {
		t.Fatal(err)
	}
	year, month, day := leap.Date()
	if year != 2024 || month != time.February || day != 29 {
		t.Fatal("date changed")
	}
	for _, text := range []string{"2023-02-29", "0000-01-01", "10000-01-01", "2024-1-1", "2024-04-31", "01/02/2024"} {
		if _, err := concepts.ParseDateOnly(text); err == nil {
			t.Errorf("accepted date %q", text)
		}
	}
	for _, text := range []string{"24:00:00", "12:60:00", "00:00:60", "0:00:00", "12:00:00.", "12:00:00.12345678", "-1:00:00"} {
		if _, err := concepts.ParseTimeOnly(text); err == nil {
			t.Errorf("accepted time %q", text)
		}
	}
	clock, err := concepts.ParseTimeOnly("01:02:03.4")
	if err != nil {
		t.Fatal(err)
	}
	if clock.Ticks() != 37_234_000_000 || clock.String() != "01:02:03.4000000" {
		t.Fatalf("time = %d / %s", clock.Ticks(), clock)
	}
	if _, err = concepts.NewTimeOnly(-1); err == nil {
		t.Fatal("accepted negative time")
	}
	if _, err = concepts.NewTimeOnly(864000000000); err == nil {
		t.Fatal("accepted time past midnight")
	}
	for _, text := range []string{"10675199.02:48:05.4775808", "-10675199.02:48:05.4775809", "999999999999999999999.00:00:00", "24:00:00", "PT1S", "+00:00:00", "--00:00:00"} {
		if _, err := concepts.ParseTimeSpan(text); err == nil {
			t.Errorf("accepted duration %q", text)
		}
	}
	for _, ticks := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64, 10_000_000, -864000000000} {
		value := concepts.TimeSpan(ticks)
		parsed, err := concepts.ParseTimeSpan(value.String())
		if err != nil || parsed.Ticks() != ticks {
			t.Fatalf("round trip %d: %v %v", ticks, parsed, err)
		}
	}
}

func TestScalarCodecsAndFailureDoesNotMutate(t *testing.T) {
	for _, value := range []interface {
		encoding.TextMarshaler
		encoding.TextUnmarshaler
		json.Marshaler
		json.Unmarshaler
	}{new(concepts.UUID), new(concepts.DateOnly), new(concepts.TimeOnly), new(concepts.TimeSpan)} {
		original, err := value.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		if err = value.UnmarshalText(text); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{`null`, `1`, `{}`, `"invalid"`} {
			if err = value.UnmarshalJSON([]byte(bad)); err == nil {
				t.Errorf("accepted %s for %T", bad, value)
			}
			after, err := value.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if string(original) != string(after) {
				t.Errorf("mutated %T on failure", value)
			}
		}
	}
}

func FuzzTimeSpanRoundTrip(f *testing.F) {
	for _, ticks := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64} {
		f.Add(ticks)
	}
	f.Fuzz(func(t *testing.T, ticks int64) {
		value := concepts.TimeSpan(ticks)
		parsed, err := concepts.ParseTimeSpan(value.String())
		if err != nil || parsed != value {
			t.Fatalf("%d -> %s -> %v (%v)", ticks, value.String(), parsed, err)
		}
	})
}

func FuzzUUID(f *testing.F) {
	f.Add("00112233-4455-4677-8899-aabbccddeeff")
	f.Add("invalid")
	f.Fuzz(func(t *testing.T, text string) {
		id, err := concepts.ParseUUID(text)
		if err != nil {
			return
		}
		parsed, err := concepts.ParseUUID(id.String())
		if err != nil || parsed != id {
			t.Fatalf("UUID round trip failed: %v", err)
		}
	})
}
