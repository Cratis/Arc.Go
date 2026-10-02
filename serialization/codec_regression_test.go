// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization_test

import (
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
)

func TestExactWireNames(t *testing.T) {
	type model struct {
		Name  string
		ID    string `json:"id"`
		Upper string `json:"NAME"`
	}
	for _, tc := range []struct {
		name, input string
		want        model
		duplicate   bool
	}{
		{"exact", `{"name":"Ada","id":"42","NAME":"upper"}`, model{"Ada", "42", "upper"}, false},
		{"unknown case", `{"Name":"Ada","ID":"42"}`, model{}, false},
		{"case variants coexist", `{"name":"Ada","Name":"Grace"}`, model{Name: "Ada"}, false},
		{"exact duplicate", `{"name":"Ada","name":"Grace"}`, model{}, true},
		{"unknown duplicate", `{"Name":"Ada","Name":"Grace"}`, model{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got model
			err := serialization.Unmarshal([]byte(tc.input), &got)
			var duplicate *serialization.DuplicateMemberError
			if errors.As(err, &duplicate) != tc.duplicate || !tc.duplicate && err != nil {
				t.Fatalf("error = %v, duplicate = %v", err, tc.duplicate)
			}
			if got != tc.want {
				t.Fatalf("model = %#v, want %#v", got, tc.want)
			}
		})
	}
}

type textValue struct{ Value string }

func (v textValue) MarshalText() ([]byte, error)     { return []byte(v.Value), nil }
func (v *textValue) UnmarshalText(data []byte) error { v.Value = string(data); return nil }

type pointerText struct{ Value string }

func (v *pointerText) MarshalText() ([]byte, error)    { return []byte(v.Value), nil }
func (v *pointerText) UnmarshalText(data []byte) error { v.Value = string(data); return nil }

type pointerJSON struct{ Secret string }

func (*pointerJSON) MarshalJSON() ([]byte, error)      { return []byte(`"redacted"`), nil }
func (v *pointerJSON) UnmarshalJSON(data []byte) error { return json.Unmarshal(data, &v.Secret) }

type valueJSON struct{ Secret string }

func (valueJSON) MarshalJSON() ([]byte, error)       { return []byte(`"json"`), nil }
func (valueJSON) MarshalText() ([]byte, error)       { return []byte("text"), nil }
func (v *valueJSON) UnmarshalJSON(data []byte) error { return json.Unmarshal(data, &v.Secret) }

func TestCustomCodecsBeforeStructTraversal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"text value", textValue{"Ada"}, `"Ada"`},
		{"text pointer", &pointerText{"Ada"}, `"Ada"`},
		{"addressable text field", &struct{ Value pointerText }{pointerText{"Ada"}}, `{"value":"Ada"}`},
		{"addressable text slice", []pointerText{{"Ada"}}, `["Ada"]`},
		{"stdlib text", netip.MustParseAddr("192.0.2.1"), `"192.0.2.1"`},
		{"json before text", valueJSON{"secret"}, `"json"`},
		{"json pointer", &pointerJSON{"secret"}, `"redacted"`},
		{"addressable json field", &struct{ Value pointerJSON }{pointerJSON{"secret"}}, `{"value":"redacted"}`},
		{"addressable json slice", []pointerJSON{{"secret"}}, `["redacted"]`},
		{"nil pointer", (*pointerJSON)(nil), `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := serialization.Marshal(tc.value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("Marshal = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name, input string
		want        any
	}{
		{"text value", `"Ada"`, textValue{"Ada"}},
		{"text null uses standard handling", `null`, textValue{}},
		{"nil text pointer", `null`, (*pointerText)(nil)},
		{"text pointer", `"Ada"`, &pointerText{"Ada"}},
		{"json value", `"Ada"`, valueJSON{"Ada"}},
		{"json pointer", `"Ada"`, &pointerJSON{"Ada"}},
		{"stdlib text", `"192.0.2.1"`, netip.MustParseAddr("192.0.2.1")},
		{"nested text", `{"value":"Ada"}`, struct{ Value pointerText }{pointerText{"Ada"}}},
		{"json slice", `["Ada"]`, []pointerJSON{{"Ada"}}},
	} {
		t.Run("bind "+tc.name, func(t *testing.T) {
			got := reflect.New(reflect.TypeOf(tc.want))
			if err := serialization.Unmarshal([]byte(tc.input), got.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Elem().Interface(), tc.want) {
				t.Fatalf("value = %#v, want %#v", got.Elem().Interface(), tc.want)
			}
		})
	}
	for _, input := range []string{`{}`, `4`, `true`} {
		t.Run("text rejects "+input, func(t *testing.T) {
			var got textValue
			if err := serialization.Unmarshal([]byte(input), &got); err == nil {
				t.Fatal("text codec accepted a non-string token")
			}
		})
	}
}

var errCodec = errors.New("codec rejected value")

type failingJSON struct{}

func (failingJSON) MarshalJSON() ([]byte, error) { return nil, errCodec }
func (failingJSON) UnmarshalJSON([]byte) error   { return errCodec }

type failingText struct{}

func (failingText) MarshalText() ([]byte, error) { return nil, errCodec }
func (failingText) UnmarshalText([]byte) error   { return errCodec }

func TestCustomCodecFailuresRemainVisible(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"JSON value receivers", failingJSON{}},
		{"text value receivers", failingText{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := serialization.Marshal(tc.value); !errors.Is(err, errCodec) {
				t.Fatalf("marshal error = %v, want codec failure", err)
			}
			target := reflect.New(reflect.TypeOf(tc.value)).Interface()
			if err := serialization.Unmarshal([]byte(`"input"`), target); !errors.Is(err, errCodec) {
				t.Fatalf("unmarshal error = %v, want codec failure", err)
			}
		})
	}
}

func TestPointerCodecsInResultPayloads(t *testing.T) {
	for _, tc := range []struct {
		name, member string
		value        any
	}{
		{"command", "response", commands.WithResponse(concepts.UUID{}, &struct{ Value pointerJSON }{pointerJSON{"secret"}})},
		{"query", "data", queries.Success(concepts.UUID{}, []pointerJSON{{"secret"}})},
		{"changes", "added", queries.ChangeSet{Added: []any{&pointerJSON{"secret"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := serialization.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err = json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := `["redacted"]`
			if tc.member == "response" {
				want = `{"value":"redacted"}`
			}
			if string(got[tc.member]) != want {
				t.Fatalf("payload = %s, want %s", got[tc.member], want)
			}
		})
	}
}

type embeddedBase struct {
	ID     string
	hidden string
}

// EmbeddedExported is an exported anonymous-field fixture.
type EmbeddedExported struct{ Name string }
type embeddedLeft struct{ Value string }
type embeddedRight struct{ Value string }
type embeddedTagged struct {
	Value string `json:"value"`
}

func TestEmbeddedFieldPromotion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"unexported", struct {
			embeddedBase
			Name string
		}{embeddedBase{"42", "secret"}, "Ada"}, `{"ID":"42","name":"Ada"}`},
		{"exported", struct{ EmbeddedExported }{EmbeddedExported{"Ada"}}, `{"name":"Ada"}`},
		{"pointer", struct{ *EmbeddedExported }{&EmbeddedExported{"Ada"}}, `{"name":"Ada"}`},
		{"nil pointer", struct{ *EmbeddedExported }{}, `{}`},
		{"ignored", struct {
			embeddedBase `json:"-"`
			Name         string
		}{embeddedBase{"42", "secret"}, "Ada"}, `{"name":"Ada"}`},
		{"tagged embed", struct {
			embeddedBase `json:"base"`
		}{embeddedBase{"42", "secret"}}, `{"base":{"ID":"42"}}`},
		{"shallow wins", struct {
			embeddedLeft
			Value string
		}{embeddedLeft{"hidden"}, "outer"}, `{"value":"outer"}`},
		{"ambiguous promotion", struct {
			embeddedLeft
			embeddedRight
		}{}, `{}`},
		{"tagged wins", struct {
			embeddedLeft
			embeddedTagged
		}{embeddedLeft{"hidden"}, embeddedTagged{"tagged"}}, `{"value":"tagged"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := serialization.Marshal(tc.value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("Marshal = %s, %v; want %s", got, err, tc.want)
			}
			decoded := reflect.New(reflect.TypeOf(tc.value))
			if err = serialization.Unmarshal([]byte(tc.want), decoded.Interface()); err != nil {
				t.Fatal(err)
			}
			roundTrip, err := serialization.Marshal(decoded.Interface())
			if err != nil || string(roundTrip) != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", roundTrip, err, tc.want)
			}
		})
	}
	var unexportedPointer struct{ *embeddedBase }
	if err := serialization.Unmarshal([]byte(`{"ID":"42"}`), &unexportedPointer); err == nil {
		t.Fatal("cannot allocate nil unexported embedded pointer")
	}
}

type valueZero int

func (v valueZero) IsZero() bool { return v == -1 }

type pointerZero int

func (v *pointerZero) IsZero() bool { return *v == -1 }

func TestStandardOmissionTags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"zero struct is not empty", struct {
			Nested struct {
				Count int `json:"count"`
			} `json:"nested,omitempty"`
		}{}, `{"nested":{"count":0}}`},
		{"nonempty zero array", struct {
			Value [1]int `json:"value,omitempty"`
		}{}, `{"value":[0]}`},
		{"empty array", struct {
			Value [0]int `json:"value,omitempty"`
		}{}, `{}`},
		{"empty scalar", struct {
			Value int `json:"value,omitempty"`
		}{}, `{}`},
		{"interface zero is not empty", struct {
			Value any `json:"value,omitempty"`
		}{0}, `{"value":0}`},
		{"untyped interface does not dispatch IsZero", struct {
			Value any `json:"value,omitzero"`
		}{valueZero(-1)}, `{"value":-1}`},
		{"IsZero interface sentinel", struct {
			Value interface{ IsZero() bool } `json:"value,omitzero"`
		}{valueZero(-1)}, `{}`},
		{"value IsZero sentinel", struct {
			Value valueZero `json:"value,omitzero"`
		}{-1}, `{}`},
		{"value IsZero retains zero", struct {
			Value valueZero `json:"value,omitzero"`
		}{}, `{"value":0}`},
		{"pointer IsZero boxed", struct {
			Value pointerZero `json:"value,omitzero"`
		}{-1}, `{}`},
		{"pointer IsZero addressable", &struct {
			Value pointerZero `json:"value,omitzero"`
		}{-1}, `{}`},
		{"pointer IsZero retains zero", struct {
			Value pointerZero `json:"value,omitzero"`
		}{}, `{"value":0}`},
		{"nil IsZero pointer", struct {
			Value *pointerZero `json:"value,omitzero"`
		}{}, `{}`},
		{"zero struct omitzero", struct {
			Value struct{} `json:"value,omitzero"`
		}{}, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := serialization.Marshal(tc.value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("Marshal = %s, %v; want %s", got, err, tc.want)
			}
			standard, err := json.Marshal(tc.value)
			if err != nil || string(standard) != tc.want {
				t.Fatalf("encoding/json = %s, %v; want %s", standard, err, tc.want)
			}
		})
	}
}

func TestTimestampWirePolicy(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"UTC", "2026-10-02T03:04:05Z", `"2026-10-02T03:04:05Z"`},
		{"nanoseconds", "2026-10-02T03:04:05.123456789Z", `"2026-10-02T03:04:05.123456789Z"`},
		{"offset and trailing zeros", "2026-10-02T03:04:05.123400000+02:30", `"2026-10-02T03:04:05.1234+02:30"`},
		{"zero offset becomes Z", "2026-10-02T03:04:05+00:00", `"2026-10-02T03:04:05Z"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := time.Parse(time.RFC3339Nano, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := serialization.Marshal(value)
			if err != nil || string(got) != tc.want {
				t.Fatalf("time = %s, %v; want %s", got, err, tc.want)
			}
			var decoded time.Time
			if err = serialization.Unmarshal(got, &decoded); err != nil || !decoded.Equal(value) {
				t.Fatalf("round trip = %v, %v", decoded, err)
			}
		})
	}
	for _, input := range []string{`"2026-10-02"`, `"2026-10-02T03:04"`, `"2026-10-02T03:04:05"`} {
		t.Run("unsupported "+input, func(t *testing.T) {
			var value time.Time
			if err := serialization.Unmarshal([]byte(input), &value); err == nil {
				t.Fatal("accepted non-RFC3339 timestamp")
			}
		})
	}
}

func TestNullStringPolicyAndOptionalOutput(t *testing.T) {
	type namedString string
	for _, tc := range []struct {
		name    string
		target  any
		rejects bool
	}{
		{"string", new(string), true},
		{"named string", new(namedString), true},
		{"pointer", new(*string), false},
		{"optional", new(serialization.Optional[string]), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := serialization.Unmarshal([]byte(`null`), tc.target)
			if (err != nil) != tc.rejects {
				t.Fatalf("null error = %v, rejects = %v", err, tc.rejects)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value serialization.Optional[string]
		want  string
	}{
		{"missing", serialization.Optional[string]{}, `{}`},
		{"present null", serialization.Null[string](), `{"value":null}`},
		{"empty string", serialization.Some(""), `{"value":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := serialization.Marshal(struct {
				Value serialization.Optional[string]
			}{tc.value})
			if err != nil || string(got) != tc.want {
				t.Fatalf("optional = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}
