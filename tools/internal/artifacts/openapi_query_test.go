// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cratis/arc.go/queries"
)

func openAPIQueryFixture(t *testing.T) *Graph {
	t.Helper()
	graph, err := contractGraph(t, `//arc:readmodel
type Row struct { Id int32; Score int32; Active bool; Label string }
//arc:query path=/api/plain
func (Row) Plain() ([]Row, error) { return nil, nil }
//arc:query path=/api/query-only http=QUERY
func (Row) QueryOnly() ([]Row, error) { return nil, nil }
`, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestOpenAPIQueryUsesNativeExtensionWithoutRewriting(t *testing.T) {
	graph := openAPIQueryFixture(t)
	before, _ := json.Marshal(graph)
	document, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	paths := openAPIDecode(t, document.bytes())["paths"].(map[string]any)
	for _, path := range []string{"/api/plain", "/api/query-only"} {
		item := paths[path].(map[string]any)
		if item["query"] != nil || item["post"] != nil {
			t.Fatal("invented native 3.1 method or POST alias", item)
		}
		envelope := item["x-cratis-query"].(map[string]any)
		operation := envelope["operation"].(map[string]any)
		if envelope["method"] != "QUERY" || !strings.HasSuffix(operation["operationId"].(string), ".QUERY") {
			t.Fatal(envelope)
		}
		body := operation["requestBody"].(map[string]any)
		if body["required"] != true || body["content"].(map[string]any)["application/json"] == nil {
			t.Fatal(body)
		}
		responses := operation["responses"].(map[string]any)
		for _, status := range []string{"200", "400", "403", "413", "415", "500"} {
			response := responses[status].(map[string]any)
			cache := response["headers"].(map[string]any)["Cache-Control"].(map[string]any)
			if cache["schema"].(map[string]any)["const"] != "no-store" {
				t.Fatal(status, response)
			}
		}
		if path == "/api/query-only" && (item["get"] != nil || item["head"] != nil) {
			t.Fatal("QUERY-only acquired an ordinary method", item)
		}
		if path == "/api/plain" && (item["get"] == nil || item["head"] == nil) {
			t.Fatal("dual method query lost GET/HEAD", item)
		}
	}
	after, _ := json.Marshal(graph)
	if !bytes.Equal(before, after) {
		t.Fatal("query projection mutated the graph")
	}
	slices.Reverse(graph.Endpoints)
	slices.Reverse(graph.Queries)
	again, err := renderOpenAPI(graph)
	if err != nil || !bytes.Equal(document.bytes(), again.bytes()) {
		t.Fatal("query projection was order dependent", err)
	}
}

// Parser boundaries that cannot be claimed as JSON Schema assertions remain
// explicit executable runtime witnesses, not a permissive schema bypass.
func TestOpenAPIQueryReaderBoundary(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{}`, true},
		{`{"arguments":null,"paging":null,"sorting":null}`, true},
		{`{"ARGUMENTS":{"unused":{"anything":true}}}`, true},
		{`{"paging":{"page":-2147483648,"pageSize":2147483647}}`, true},
		{`{"pagİng":{"pageSİze":1}}`, true},
		{`{"pagİng":{"pageSİze":2147483648}}`, false},
		{`null`, false},
		{`{} {}`, false},
		{`{"arguments":{},"ARGUMENTS":{}}`, false},
		{`{"arguments":{"x":1,"X":2}}`, false},
		{`{"paging":{"pageSize":2147483648}}`, false},
		{`{"paging":{"pageSize":null}}`, false},
		{`{"sorting":{"field":"id","direction":"sideways"}}`, false},
	} {
		_, err := queries.ReadQUERY([]byte(tc.body))
		if (err == nil) != tc.valid {
			t.Errorf("ReadQUERY(%s) = %v, want valid=%t", tc.body, err, tc.valid)
		}
	}
}
