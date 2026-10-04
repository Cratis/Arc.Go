// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"reflect"
	"strings"
	"testing"
)

// Source-derived fixtures, not an executed paired-C# witness. Authority: Arc
// 7c1e78075b737df64f69fddfaae83374f75e3612,
// Source/DotNET/Arc/ModelBinding/FromRequestModelBinder.cs, BindModelAsync and
// DefaultValueChecker<T>; Arc.Specs/ModelBinding/for_FromRequestModelBinder/
// when_model_is_provided_partially_by_both_binders.cs. The source fills a default
// body property ONLY from a nondefault request property. String default is null,
// not empty; nullable zero/false is nondefault. These are intentionally not
// generic "body presence wins" or "query always wins" fixtures.
func TestActionRequestPinnedDefaultMerge(t *testing.T) {
	type input struct {
		Count int     `json:"count"`
		Flag  bool    `json:"flag"`
		Name  string  `json:"name"`
		Maybe *int    `json:"maybe"`
		Text  *string `json:"text"`
	}
	zero, seven, empty := 0, 7, ""
	request := input{Count: 43, Flag: true, Name: "request", Maybe: &seven, Text: &empty}
	cases := []struct {
		name string
		body string
		want input
	}{
		{"missing body uses request", "", request},
		{"missing members use request", `{}`, request},
		{"zero and false use request", `{"count":0,"flag":false}`, request},
		{"body nondefaults win", `{"count":42,"flag":true,"name":"body"}`, input{42, true, "body", &seven, &empty}},
		{"empty string is not null", `{"name":""}`, input{43, true, "", &seven, &empty}},
		{"null string uses request", `{"name":null}`, request},
		{"nullable zero wins", `{"maybe":0}`, input{43, true, "request", &zero, &empty}},
		{"nullable null uses request", `{"maybe":null,"text":null}`, request},
		// Go-specific: retain the existing serializer's exact wire-name matching.
		{"unknown differently cased member is ignored", `{"NAME":""}`, request},
	}
	bind, err := newActionRequest[input](true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bind([]byte(tc.body), request)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, tc.want)
			}
			if got.Maybe != nil {
				*got.Maybe = 99
				if seven != 7 {
					t.Fatal("merged value aliases request input")
				}
			}
		})
	}
}

func TestActionRequestDefaultRequestDoesNotReplaceBody(t *testing.T) {
	type input struct {
		Count int  `json:"count"`
		Flag  bool `json:"flag"`
	}
	bind, err := newActionRequest[input](true)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"count":42,"flag":true}`, `{"count":0,"flag":false}`} {
		got, err := bind([]byte(body), input{})
		if err != nil {
			t.Fatal(err)
		}
		if body == `{"count":42,"flag":true}` && got != (input{42, true}) || body == `{"count":0,"flag":false}` && got != (input{}) {
			t.Fatalf("unexpected merge: %#v", got)
		}
	}
}

func TestActionRequestRejectsUnsupportedMergeShape(t *testing.T) {
	type input struct{ Nested struct{ Count int } }
	if _, err := newActionRequest[input](true); err == nil {
		t.Fatal("nested merging was silently admitted")
	}
}

func TestActionRequestRejectsMalformedBodies(t *testing.T) {
	type input struct{ Count int }
	bind, err := newActionRequest[input](true)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`null`, `[]`, `{`, `{} {}`, `{"count":"no"}`, `{"count":1,"count":2}`, " \n\t", `{"unknown":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`} {
		if _, err := bind([]byte(body), input{Count: 4}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
