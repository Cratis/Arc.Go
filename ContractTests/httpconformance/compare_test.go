// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"net/http"
	"slices"
	"testing"
)

func TestFixedCompleteInventory(t *testing.T) {
	cases := corpus()
	if err := validateCorpus(cases); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]requestCase{nil, cases[:len(cases)-1], append(slices.Clone(cases[:len(cases)-1]), cases[0])} {
		if validateCorpus(bad) == nil {
			t.Fatal("partial or duplicated inventory admitted")
		}
	}
	changed := slices.Clone(cases)
	changed[0].Body = "different"
	if validateCorpus(changed) == nil {
		t.Fatal("substituted inventory admitted")
	}
}
func TestComparisonNormalizesOnlyObjectOrderAndWhitespace(t *testing.T) {
	a := exchange{200, http.Header{"Content-Type": []string{"application/json"}}, []byte(`{"data":[{"id":1}],"paging":{"totalItems":4}}`)}
	b := a
	b.Body = []byte(" { \"paging\": {\"totalItems\":4}, \"data\": [{\"id\":1}] } ")
	if got := compare(a, b); len(got) != 0 {
		t.Fatal(got)
	}
}
func TestComparisonDetectsContractChanges(t *testing.T) {
	for name, pair := range map[string][2]string{
		"missing-null": {`{}`, `{"data":null}`}, "array-order": {`[1,2]`, `[2,1]`},
		"paging":        {`{"paging":{"totalItems":4}}`, `{"paging":{"totalItems":2}}`},
		"full-envelope": {`{"isReady":true}`, `{"isReady":false}`},
		"integer-width": {`9007199254740992`, `9007199254740993`},
		"extra-field":   {`{"data":[]}`, `{"data":[],"extra":true}`},
	} {
		t.Run(name, func(t *testing.T) {
			if len(compare(exchange{Body: []byte(pair[0])}, exchange{Body: []byte(pair[1])})) == 0 {
				t.Fatal("difference admitted")
			}
		})
	}
	if len(compare(exchange{Status: 200, Body: []byte(`{}`)}, exchange{Status: 400, Body: []byte(`{}`)})) == 0 {
		t.Fatal("status ignored")
	}
	for _, name := range relevantHeaders {
		a := exchange{Header: http.Header{}, Body: []byte(`{}`)}
		b := a
		b.Header = make(http.Header)
		b.Header.Set(name, "different")
		if len(compare(a, b)) == 0 {
			t.Fatalf("header %s ignored", name)
		}
	}
}
func TestMalformedBodiesFailComparison(t *testing.T) {
	for _, body := range []string{`{"id":1,"id":2}`, `{} {}`, `[`, ``} {
		if len(compare(exchange{Body: []byte(body)}, exchange{Body: []byte(body)})) == 0 {
			t.Errorf("admitted %q", body)
		}
	}
}
func TestFixtureCopiesMembershipAndHasUniqueSortValues(t *testing.T) {
	first := rows()
	first[0].Score = 99
	second := rows()
	if second[0].Score != 10 {
		t.Fatal("shared mutable fixture")
	}
	seen := map[int]bool{}
	for _, r := range second {
		if seen[r.Score] {
			t.Fatal("ambiguous sort fixture")
		}
		seen[r.Score] = true
	}
}
