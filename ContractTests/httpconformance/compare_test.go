// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
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
func plainControlPair(t *testing.T, c requestCase) (exchange, exchange) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"paging":        map[string]int{"page": 1, "size": 2, "totalItems": 0, "totalPages": 0},
		"correlationId": correlationID, "data": rows(), "isSuccess": true,
		"isReady": true, "isAuthorized": true, "isValid": true, "hasExceptions": false,
		"validationResults": []any{}, "exceptionMessages": []string{}, "exceptionStackTrace": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	a := exchange{Status: 200, Header: http.Header{}, Body: body}
	a.Header.Set("Content-Type", "application/json; charset=utf-8")
	a.Header.Set("X-Correlation-ID", correlationID)
	if c.Method == "QUERY" {
		a.Header.Set("Cache-Control", "no-store")
	}
	b := a
	b.Header = a.Header.Clone()
	b.Body = []byte(strings.Replace(string(body), `"page":1,"size":2`, `"page":0,"size":0`, 1))
	return a, b
}

func TestOrdinaryListAllowanceIsExactAndKeepsRawDifferences(t *testing.T) {
	allowed := 0
	for _, c := range corpus() {
		if c.Group != "plain-paging-control" {
			continue
		}
		allowed++
		a, b := plainControlPair(t, c)
		raw := compare(a, b)
		accepted, unaccepted := disposition(c, a, b)
		want := []allowance{{"ordinary-list-unpaged", "$.paging.page"}, {"ordinary-list-unpaged", "$.paging.size"}}
		if len(raw) != 2 || len(unaccepted) != 0 || !reflect.DeepEqual(accepted, want) || !reflect.DeepEqual(compare(a, b), raw) {
			t.Fatalf("%s: raw=%v accepted=%v unaccepted=%v", c.Name, raw, accepted, unaccepted)
		}
	}
	if allowed != 2 {
		t.Fatalf("allowance inventory = %d", allowed)
	}
}

func TestOrdinaryListAllowanceRejectsChangedRequestAndOutcome(t *testing.T) {
	for _, c := range corpus() {
		if c.Group != "plain-paging-control" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			for _, field := range []string{"group", "name", "method", "path", "body"} {
				t.Run(field, func(t *testing.T) {
					a, b := plainControlPair(t, c)
					changed := c
					switch field {
					case "group":
						changed.Group = "Plain-paging-control"
					case "name":
						changed.Name = "Page/" + c.Method
					case "method":
						changed.Method = strings.ToLower(c.Method)
					case "path":
						changed.Path += "&extra=1"
					case "body":
						changed.Body += " "
					}
					assertNoAllowance(t, changed, a, b)
				})
			}
			for name, change := range map[string]func(*exchange){
				"status": func(e *exchange) { e.Status = 400 },
				"page":   func(e *exchange) { e.Body = []byte(strings.ReplaceAll(string(e.Body), `"page":1`, `"page":3`)) },
				"size":   func(e *exchange) { e.Body = []byte(strings.ReplaceAll(string(e.Body), `"size":2`, `"size":4`)) },
				"total-items": func(e *exchange) {
					e.Body = []byte(strings.ReplaceAll(string(e.Body), `"totalItems":0`, `"totalItems":4`))
				},
				"total-pages": func(e *exchange) {
					e.Body = []byte(strings.ReplaceAll(string(e.Body), `"totalPages":0`, `"totalPages":2`))
				},
				"order": func(e *exchange) {
					v, err := decode(e.Body)
					if err != nil {
						t.Fatal(err)
					}
					m := v.(map[string]any)
					data := m["data"].([]any)
					data[0], data[1] = data[1], data[0]
					e.Body, err = json.Marshal(m)
					if err != nil {
						t.Fatal(err)
					}
				},
				"extra-error": func(e *exchange) {
					e.Body = []byte(strings.ReplaceAll(string(e.Body), `"exceptionMessages":[]`, `"exceptionMessages":["unexpected"]`))
				},
				"extra-field": func(e *exchange) {
					e.Body = []byte(strings.ReplaceAll(string(e.Body), `"isReady":true`, `"isReady":true,"changeSet":{}`))
				},
				"success-flag": func(e *exchange) {
					e.Body = []byte(strings.ReplaceAll(string(e.Body), `"isSuccess":true`, `"isSuccess":false`))
				},
			} {
				t.Run(name, func(t *testing.T) {
					a, b := plainControlPair(t, c)
					change(&a)
					assertNoAllowance(t, c, a, b)
					// Shared wrong outcomes must not be accepted just because only
					// the original two paging paths remain different.
					if name != "page" && name != "size" {
						change(&b)
						assertNoAllowance(t, c, a, b)
					}
				})
			}
			for _, header := range relevantHeaders {
				a, b := plainControlPair(t, c)
				b.Header.Set(header, "changed")
				assertNoAllowance(t, c, a, b)
			}
			a, b := plainControlPair(t, c)
			b.Body = []byte(strings.ReplaceAll(string(b.Body), `"page":0`, `"page":1`))
			assertNoAllowance(t, c, a, b)
		})
	}
}

func assertNoAllowance(t *testing.T, c requestCase, a, b exchange) {
	t.Helper()
	accepted, unaccepted := disposition(c, a, b)
	if len(accepted) != 0 || len(unaccepted) == 0 || !reflect.DeepEqual(unaccepted, compare(a, b)) {
		t.Fatalf("changed exchange admitted: %v / %v", accepted, unaccepted)
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
