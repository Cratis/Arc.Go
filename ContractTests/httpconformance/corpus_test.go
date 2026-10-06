// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"fmt"
	"net/url"
)

const correlationID = "00112233-4455-4677-8899-aabbccddeeff"
const groupCount = 14
const caseCount = 36

type requestCase struct{ Group, Name, Method, Path, Body string }

// Each request is immutable and sent to BOTH hosts. Null is a QUERY-only JSON
// value; GET's literal "null" deliberately remains a string, not fabricated null.
func corpus() []requestCase {
	type variant struct{ group, name, path, query, body string }
	variants := []variant{
		{"plain-baseline", "baseline", "plain", "", "{}"},
		{"renderable-baseline", "baseline", "renderable", "", "{}"},
		{"plain-paging-control", "page", "plain", "page=1&pageSize=2", `{"paging":{"page":1,"pageSize":2}}`},
		{"count-before-window-ascending", "page", "renderable", "page=1&pageSize=2&sortby=score&sortDirection=ascending", `{"paging":{"page":1,"pageSize":2},"sorting":{"field":"score","direction":"ascending"}}`},
		{"count-before-window-descending", "page", "renderable", "page=1&pageSize=2&sortby=score&sortDirection=descending", `{"paging":{"page":1,"pageSize":2},"sorting":{"field":"score","direction":"descending"}}`},
		{"empty-selection", "empty", "filter", "min=100", `{"arguments":{"min":100}}`},
		{"out-of-range-page", "empty", "renderable", "page=10&pageSize=2", `{"paging":{"page":10,"pageSize":2}}`},
		{"scalar-binding", "scalars", "filter", "min=20&active=false&prefix=row", `{"arguments":{"min":20,"active":false,"prefix":"row"}}`},
		{"binding-defaults", "missing", "filter", "", "{}"},
		{"binding-defaults", "empty", "filter", "min=&active=&prefix=", `{"arguments":{"min":"","active":"","prefix":""}}`},
		{"binding-defaults", "null", "filter", "prefix=null", `{"arguments":{"min":null,"active":null,"prefix":null}}`},
		{"malformed-page", "bad", "renderable", "page=no&pageSize=2", `{"paging":{"page":"no","pageSize":2}}`},
		{"malformed-size", "bad", "renderable", "page=1&pageSize=no", `{"paging":{"page":1,"pageSize":"no"}}`},
		{"nonpositive-size", "zero", "renderable", "page=1&pageSize=0", `{"paging":{"page":1,"pageSize":0}}`},
		{"nonpositive-size", "negative", "renderable", "page=1&pageSize=-2", `{"paging":{"page":1,"pageSize":-2}}`},
		{"invalid-sort-direction", "bad", "renderable", "sortby=score&sortDirection=no", `{"sorting":{"field":"score","direction":"no"}}`},
		{"failure-with-paging", "page", "failing", "page=1&pageSize=2", `{"paging":{"page":1,"pageSize":2}}`},
		{"failure-with-paging", "unpaged", "failing", "", "{}"},
	}
	result := make([]requestCase, 0, len(variants)*2)
	for _, v := range variants {
		path := "/api/" + v.path
		get := path
		if v.query != "" {
			get += "?" + v.query
		}
		result = append(result, requestCase{v.group, v.name + "/GET", "GET", get, ""}, requestCase{v.group, v.name + "/QUERY", "QUERY", path, v.body})
	}
	return result
}

func validateCorpus(cases []requestCase) error {
	if len(cases) != caseCount {
		return fmt.Errorf("partial inventory: got %d cases, require %d", len(cases), caseCount)
	}
	groups := map[string]map[string]bool{}
	seen := map[string]bool{}
	for _, c := range cases {
		key := c.Group + "/" + c.Name
		if seen[key] || c.Group == "" || c.Name == "" {
			return fmt.Errorf("duplicate or empty inventory entry: %s", key)
		}
		seen[key] = true
		if c.Method != "GET" && c.Method != "QUERY" {
			return fmt.Errorf("unsupported method %s", c.Method)
		}
		u, err := url.Parse(c.Path)
		if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || len(c.Path) < 5 || c.Path[:5] != "/api/" {
			return fmt.Errorf("invalid corpus path %q", c.Path)
		}
		if groups[c.Group] == nil {
			groups[c.Group] = map[string]bool{}
		}
		groups[c.Group][c.Method] = true
	}
	if len(groups) != groupCount {
		return fmt.Errorf("partial group inventory: got %d, require %d", len(groups), groupCount)
	}
	for name, methods := range groups {
		if !methods["GET"] || !methods["QUERY"] {
			return fmt.Errorf("group %s requires GET and QUERY", name)
		}
	}
	// Equal cardinality alone cannot admit substituted requests or groups.
	for i, want := range corpus() {
		if cases[i] != want {
			return fmt.Errorf("inventory entry %d differs from fixed corpus", i)
		}
	}
	return nil
}
