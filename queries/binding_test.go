// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

type bindArgs struct {
	Required int                            `json:"required" query:"required"`
	Limit    int                            `json:"limit" query:"default=10"`
	Name     string                         `json:"name"`
	Maybe    serialization.Optional[string] `json:"maybe" query:"preservePresence"`
	Numbers  []int64                        `json:"numbers"`
	ID       *concepts.UUID                 `json:"id"`
}
type bindingCapture struct {
	Last  bindArgs
	Calls int
}

func bindingPipeline(t *testing.T, c *bindingCapture) queries.Pipeline {
	t.Helper()
	var r queries.Registry
	mustRegister(t, queries.Register[Item](&r, "Bind", queries.Function(func(_ context.Context, a bindArgs) (Item, error) {
		c.Last = a
		c.Calls++
		return Item{Name: a.Name}, nil
	}), public[bindArgs]()))
	return build(t, &r, queries.PipelineOptions{})
}

type boolArguments struct {
	Flag bool `json:"flag"`
}

func TestBooleanBindingAcceptsOnlyTrimmedTrueFalseAcrossReaders(t *testing.T) {
	var registry queries.Registry
	var captured boolArguments
	calls := 0
	mustRegister(t, queries.Register[Item](&registry, "Boolean", queries.Function(func(_ context.Context, input boolArguments) (Item, error) {
		captured = input
		calls++
		return Item{}, nil
	})))
	p := build(t, &registry, queries.PipelineOptions{})
	for _, tc := range []struct {
		text         string
		valid, value bool
	}{
		{"true", true, true}, {" TrUe ", true, true},
		{"false", true, false}, {" FALSE ", true, false},
		{"1", false, false}, {"0", false, false}, {"t", false, false}, {"F", false, false},
	} {
		for _, reader := range []string{"direct", "GET", "QUERY"} {
			var request queries.Request
			var err error
			switch reader {
			case "GET":
				request, err = queries.ReadGET(url.Values{"flag": {tc.text}})
			case "QUERY":
				body, marshalErr := json.Marshal(map[string]any{"arguments": map[string]any{"flag": tc.text}})
				mustRegister(t, marshalErr)
				request, err = queries.ReadQUERY(body)
			default:
				arguments, argumentsErr := queries.NewArguments(map[string]any{"flag": tc.text})
				mustRegister(t, argumentsErr)
				request = queries.NewRequest(arguments, queries.Parameters{})
			}
			mustRegister(t, err)
			before := calls
			result, err := p.Perform(t.Context(), "Item.Boolean", request)
			if tc.valid {
				if err != nil || !result.IsSuccess() || calls != before+1 || captured.Flag != tc.value {
					t.Fatal(reader, tc.text, result.Details(), err, captured)
				}
			} else if err == nil || result.IsValid() || calls != before || result.Details().ValidationResults[0].Reason != validation.MalformedRequest {
				t.Fatal(reader, "accepted invalid boolean", tc.text, result.Details(), err)
			}
		}
	}
}

func TestDirectEmptyScalarInputUsesDefaultAndRequirednessRules(t *testing.T) {
	var capture bindingCapture
	p := bindingPipeline(t, &capture)
	for _, empty := range []any{nil, ""} {
		arguments, err := queries.NewArguments(map[string]any{"required": 0, "limit": empty, "name": "", "maybe": ""})
		mustRegister(t, err)
		result, err := p.Perform(t.Context(), "Item.Bind", queries.NewRequest(arguments, queries.Parameters{}))
		mustRegister(t, err)
		if !result.IsSuccess() || capture.Last.Limit != 10 || capture.Last.Name != "" || !capture.Last.Maybe.IsPresent() {
			t.Fatal("direct empty numeric input did not use default", result.Details(), capture.Last)
		}
		before := capture.Calls
		arguments, err = queries.NewArguments(map[string]any{"required": empty})
		mustRegister(t, err)
		result, err = p.Perform(t.Context(), "Item.Bind", queries.NewRequest(arguments, queries.Parameters{}))
		var argumentError *queries.ArgumentError
		if !errors.As(err, &argumentError) || !argumentError.Missing || result.IsValid() || capture.Calls != before {
			t.Fatal("direct empty required input was not classified missing", result.Details(), err)
		}
		if findings := result.Details().ValidationResults; len(findings) != 1 || findings[0].Reason != validation.Rule {
			t.Fatal(findings)
		}
	}
}

func TestBindingPresenceDefaultsZeroAndAtomicFailure(t *testing.T) {
	var capture bindingCapture
	p := bindingPipeline(t, &capture)
	cases := []struct {
		name   string
		body   string
		reason validation.Reason
		calls  int
	}{
		{"missing required", `{"arguments":{}}`, validation.Rule, 0},
		{"zero is supplied", `{"arguments":{"required":0}}`, "", 1},
		{"null omitted", `{"arguments":{"required":null}}`, validation.Rule, 0},
		{"empty omitted", `{"arguments":{"required":""}}`, validation.Rule, 0},
		{"malformed supplied", `{"arguments":{"required":"bad"}}`, validation.MalformedRequest, 0},
		{"fraction rejected", `{"arguments":{"required":1.5}}`, validation.MalformedRequest, 0},
		{"overflow rejected", `{"arguments":{"required":9223372036854775808}}`, validation.MalformedRequest, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := capture.Calls
			request, err := queries.ReadQUERY([]byte(tc.body))
			mustRegister(t, err)
			result, err := p.Perform(context.Background(), "Item.Bind", request)
			if capture.Calls-before != tc.calls {
				t.Fatalf("calls = %d", capture.Calls-before)
			}
			if tc.reason == "" {
				if err != nil || !result.IsSuccess() || capture.Last.Limit != 10 {
					t.Fatalf("%+v %v %+v", result.Details(), err, capture.Last)
				}
			} else {
				findings := result.Details().ValidationResults
				if len(findings) != 1 || findings[0].Reason != tc.reason || findings[0].Members[0] != "required" {
					t.Fatalf("findings = %+v", findings)
				}
				if _, ok := result.Data(); ok {
					t.Fatal("failed data published")
				}
			}
		})
	}
}
func TestBindingTransportRawNullEmptyAndTypedDistinctions(t *testing.T) {
	var capture bindingCapture
	p := bindingPipeline(t, &capture)
	for _, node := range []string{`null`, `""`, `"supplied"`} {
		request, err := queries.ReadQUERY([]byte(`{"arguments":{"required":1,"maybe":` + node + `}}`))
		mustRegister(t, err)
		raw, present := request.Arguments().Get("MAYBE")
		if !present || string(raw.(json.RawMessage)) != node {
			t.Fatalf("raw = %v, %v", raw, present)
		}
		result, err := p.Perform(context.Background(), "Item.Bind", request)
		if err != nil || !result.IsSuccess() {
			t.Fatalf("%+v %v", result.Details(), err)
		}
		if !capture.Last.Maybe.IsPresent() || capture.Last.Maybe.IsNull() != (node == "null") {
			t.Fatal("Optional presence lost")
		}
	}
	typed := bindArgs{Limit: 0, Maybe: serialization.Some("")}
	result, err := p.Perform(context.Background(), "Item.Bind", queries.RequestFor(typed, queries.Parameters{}))
	if err != nil || !result.IsSuccess() || capture.Last.Limit != 0 {
		t.Fatal("typed request applied transport defaults", err)
	}
	raw, err := queries.NewArguments(map[string]any{"required": "", "limit": 0})
	mustRegister(t, err)
	result, err = p.Perform(context.Background(), "Item.Bind", queries.NewRequest(raw, queries.Parameters{}))
	if err == nil || result.IsValid() || result.Details().ValidationResults[0].Reason != validation.Rule {
		t.Fatal("direct empty required int was not classified missing")
	}
}
func TestBindingCSVVersusJSONArraysAndPrecision(t *testing.T) {
	var capture bindingCapture
	p := bindingPipeline(t, &capture)
	request, err := queries.ReadGET(url.Values{"required": {"1"}, "numbers": {" 1, 2 ", "9007199254740993"}, "id": {"8d15c2bf-7991-4c1b-8768-a5c852e725f2"}})
	mustRegister(t, err)
	result, err := p.Perform(context.Background(), "Item.Bind", request)
	if err != nil || !result.IsSuccess() {
		t.Fatalf("%+v %v", result.Details(), err)
	}
	if !reflect.DeepEqual(capture.Last.Numbers, []int64{1, 2, 9007199254740993}) || capture.Last.ID == nil {
		t.Fatalf("arguments = %+v", capture.Last)
	}
	request, err = queries.ReadQUERY([]byte(`{"arguments":{"required":1,"numbers":[9223372036854775807,"-2"]}}`))
	mustRegister(t, err)
	result, err = p.Perform(context.Background(), "Item.Bind", request)
	if err != nil || !result.IsSuccess() || !reflect.DeepEqual(capture.Last.Numbers, []int64{math.MaxInt64, -2}) {
		t.Fatalf("arguments = %+v, %v", capture.Last, err)
	}
	request, err = queries.ReadQUERY([]byte(`{"arguments":{"required":1,"numbers":[[1]]}}`))
	mustRegister(t, err)
	result, err = p.Perform(context.Background(), "Item.Bind", request)
	if err == nil || result.IsValid() {
		t.Fatal("nested scalar collection accepted")
	}
}

type explicitArgs struct {
	Token string            `json:"token"`
	Nodes []json.RawMessage `json:"nodes"`
}

func TestExplicitCodecsReplaceFieldsAndJSONNodeEscapeHatch(t *testing.T) {
	var r queries.Registry
	var captured explicitArgs
	mustRegister(t, queries.Register[Item](&r, "Custom", queries.Function(func(_ context.Context, a explicitArgs) (Item, error) { captured = a; return Item{}, nil }), public[explicitArgs](), queries.WithArguments(
		queries.BindArgument("token", func(a *explicitArgs, s string) { a.Token = "decoded:" + s }, queries.ArgumentOptions[string]{Required: true, DecodeText: func(s string) (string, error) { return s, nil }}),
		queries.BindArgument("nodes", func(a *explicitArgs, n []json.RawMessage) { a.Nodes = n }, queries.ArgumentOptions[[]json.RawMessage]{DecodeJSON: func(b []byte) ([]json.RawMessage, error) {
			var n []json.RawMessage
			err := json.Unmarshal(b, &n)
			return n, err
		}}),
	)))
	p := build(t, &r, queries.PipelineOptions{})
	request, err := queries.ReadQUERY([]byte(`{"arguments":{"token":"a","nodes":[{},[],null]}}`))
	mustRegister(t, err)
	result, err := p.Perform(context.Background(), "Item.Custom", request)
	if err != nil || !result.IsSuccess() || captured.Token != "decoded:a" || len(captured.Nodes) != 3 {
		t.Fatalf("%+v, %+v, %v", captured, result.Details(), err)
	}
}

type collisionArgs struct {
	One int `json:"NAME"`
	Two int `json:"name"`
}
type reservedArgs struct {
	Page int `json:"page"`
}
type nestedArgs struct {
	Nested Item `json:"nested"`
}
type defaultArgs struct {
	Bad int `query:"default=no"`
}

func TestInvalidArgumentModelsFailBeforeActivation(t *testing.T) {
	var r queries.Registry
	tests := []error{
		queries.Register[Item](&r, "Collision", queries.Function(func(context.Context, collisionArgs) (Item, error) { return Item{}, nil })),
		queries.Register[Item](&r, "Reserved", queries.Function(func(context.Context, reservedArgs) (Item, error) { return Item{}, nil })),
		queries.Register[Item](&r, "Nested", queries.Function(func(context.Context, nestedArgs) (Item, error) { return Item{}, nil })),
		queries.Register[Item](&r, "Default", queries.Function(func(context.Context, defaultArgs) (Item, error) { return Item{}, nil })),
	}
	for _, err := range tests {
		if err == nil {
			t.Fatal("invalid argument model accepted")
		}
	}
	if _, err := queries.NewArguments(map[string]any{"a": 1, "A": 2}); !errors.Is(err, queries.ErrInvalidArguments) {
		t.Fatal(err)
	}
}
func TestArgumentsCopyMembershipAndRawBytes(t *testing.T) {
	bytes := json.RawMessage(`"original"`)
	input := map[string]any{"value": bytes}
	a, err := queries.NewArguments(input)
	mustRegister(t, err)
	bytes[1] = 'X'
	delete(input, "value")
	entries := a.Entries()
	entries["value"].(json.RawMessage)[1] = 'Y'
	value, ok := a.Get("VALUE")
	if !ok || string(value.(json.RawMessage)) != `"original"` {
		t.Fatalf("value = %s", value)
	}
}

type privateConcept struct{ value int }

func (v privateConcept) ConceptValue() int            { return v.value }
func (v privateConcept) MarshalText() ([]byte, error) { return []byte(strconv.Itoa(v.value)), nil }
func (v *privateConcept) UnmarshalText(b []byte) error {
	n, err := strconv.Atoi(string(b))
	if err == nil {
		v.value = n
	}
	return err
}
func (v privateConcept) MarshalJSON() ([]byte, error) { return json.Marshal(v.value) }
func (v *privateConcept) UnmarshalJSON(b []byte) error {
	var n int
	err := json.Unmarshal(b, &n)
	if err == nil {
		v.value = n
	}
	return err
}

type conceptArgs struct {
	Code  privateConcept `json:"code"`
	Label string         `json:"label" query:"default=a\\,b\\=c"`
}

func TestPrivateConceptArgumentUsesTextCodecAndGraphValidator(t *testing.T) {
	var r queries.Registry
	calls := 0
	mustRegister(t, queries.Register[Item](&r, "Concept", queries.Function(func(_ context.Context, a conceptArgs) (Item, error) {
		calls++
		if a.Code.value != 42 || a.Label != "a,b=c" {
			t.Fatalf("bound = %+v", a)
		}
		return Item{}, nil
	}), public[conceptArgs]()))
	var validators validation.Registry
	mustRegister(t, validation.RegisterConcept(&validators, validation.ValidatorFunc[privateConcept](func(_ context.Context, v privateConcept) ([]validation.Result, error) {
		if v.value == 42 {
			return nil, nil
		}
		return []validation.Result{{Severity: validation.Error, Message: "bad code", Members: []string{"value"}}}, nil
	})))
	graph, err := validators.Build()
	mustRegister(t, err)
	p := build(t, &r, queries.PipelineOptions{Validation: graph})
	for _, value := range []string{"42", "1"} {
		request, err := queries.ReadGET(url.Values{"code": {value}})
		mustRegister(t, err)
		result, err := p.Perform(context.Background(), "Item.Concept", request)
		if err != nil {
			t.Fatal(err)
		}
		if value == "42" && !result.IsSuccess() {
			t.Fatalf("%+v", result.Details())
		}
		if value == "1" {
			findings := result.Details().ValidationResults
			if len(findings) != 1 || !reflect.DeepEqual(findings[0].Members, []string{"code"}) {
				t.Fatalf("findings = %+v", findings)
			}
		}
	}
	if calls != 1 {
		t.Fatal("failed concept reached performer")
	}
}

func FuzzArgumentConversion(f *testing.F) {
	f.Add("0")
	f.Add("9223372036854775808")
	f.Add("1,2")
	f.Add("")
	var r queries.Registry
	if err := queries.Register[Item](&r, "Bind", queries.Function(func(context.Context, bindArgs) (Item, error) { return Item{}, nil }), public[bindArgs]()); err != nil {
		f.Fatal(err)
	}
	p, err := r.Build(queries.PipelineOptions{})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, text string) {
		request, err := queries.ReadGET(url.Values{"required": {text}, "numbers": {text}})
		if err != nil {
			return
		}
		result, _ := p.Perform(t.Context(), "Item.Bind", request)
		if !result.IsSuccess() {
			if _, present := result.Data(); present {
				t.Fatal("failure published data")
			}
		}
	})
}
