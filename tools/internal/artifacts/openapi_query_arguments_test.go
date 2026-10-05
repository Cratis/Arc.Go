// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
)

const openAPIArgumentsSource = `import "github.com/cratis/arc.go/queries"
//arc:readmodel
type Row struct { Id int32 ` + "`sortable:\"true\"`" + `; Label string ` + "`sortable:\"true\"`" + ` }
type Filter struct {
	Name string ` + "`query:\"required\"`" + `
	Limit int32 ` + "`query:\"default=5\"`" + `
	Tags []string
	Flag *bool
}
//arc:query path=/api/rows
func (Row) Search(Filter) (queries.Page[Row], error) { return queries.Page[Row]{}, nil }
`

func openAPIArgumentsFixture(t *testing.T) *Graph {
	t.Helper()
	graph, err := contractGraph(t, openAPIArgumentsSource, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func openAPIOperationParameters(t *testing.T, operation map[string]any) map[string]map[string]any {
	t.Helper()
	result := map[string]map[string]any{}
	for _, value := range operation["parameters"].([]any) {
		parameter := value.(map[string]any)
		result[parameter["name"].(string)] = parameter
	}
	return result
}

func TestOpenAPIQueryArgumentsAndPagingArePublishedForBothReaders(t *testing.T) {
	graph := openAPIArgumentsFixture(t)
	document, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	item := openAPIDecode(t, document.bytes())["paths"].(map[string]any)["/api/rows"].(map[string]any)
	for _, method := range []string{"get", "head"} {
		parameters := openAPIOperationParameters(t, item[method].(map[string]any))
		for _, name := range []string{"name", "limit", "tags", "flag", "page", "pageSize", "sortBy", "sortDirection"} {
			if parameters[name] == nil || parameters[name]["in"] != "query" {
				t.Fatalf("%s lacks query parameter %s: %v", method, name, parameters)
			}
		}
		if parameters["name"]["required"] != true || parameters["limit"]["required"] != false {
			t.Fatal("requiredness was not taken from the binding", parameters["name"], parameters["limit"])
		}
		if !strings.Contains(parameters["limit"]["description"].(string), "declared default") {
			t.Fatal("default policy omitted", parameters["limit"])
		}
		tags := parameters["tags"]
		if tags["style"] != "form" || tags["explode"] != false || tags["schema"].(map[string]any)["type"] != "array" {
			t.Fatal("collection argument lost its comma encoding", tags)
		}
		if parameters["flag"]["schema"].(map[string]any)["type"] != "boolean" {
			t.Fatal("pointer argument lost its scalar schema", parameters["flag"])
		}
		limit := parameters["limit"]["schema"].(map[string]any)
		if limit["type"] != "integer" || limit["minimum"] != json.Number("-2147483648") {
			t.Fatal("integer argument lost its exact width", limit)
		}
		fields := parameters["sortBy"]["schema"].(map[string]any)["x-cratis-sort-fields"]
		if !reflect.DeepEqual(fields, []any{"id", "label"}) {
			t.Fatal("sortable fields missing from sortBy", fields)
		}
	}
	operation := item["x-cratis-query"].(map[string]any)["operation"].(map[string]any)
	if len(operation["parameters"].([]any)) != 1 {
		t.Fatal("QUERY must carry arguments in its body, not query parameters", operation["parameters"])
	}
	schema := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	arguments := schema["properties"].(map[string]any)["arguments"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
	properties := arguments["properties"].(map[string]any)
	for _, name := range []string{"name", "limit", "tags", "flag"} {
		if properties[name] == nil {
			t.Fatalf("QUERY body lacks argument %s: %v", name, properties)
		}
	}
	if !reflect.DeepEqual(arguments["x-cratis-required-arguments"], []any{"name"}) || arguments["required"] != nil {
		t.Fatal("required arguments must be stated without a case-sensitive JSON Schema required list", arguments)
	}
	if arguments["patternProperties"].(map[string]any)["^[nN][aA][mM][eE]$"] == nil {
		t.Fatal("argument case aliases are not described", arguments["patternProperties"])
	}
	sorting := schema["properties"].(map[string]any)["sorting"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(sorting["x-cratis-sort-fields"], []any{"id", "label"}) {
		t.Fatal("QUERY sorting lost sortable fields", sorting)
	}
	if !strings.Contains(schema["description"].(string), "Required arguments (name)") {
		t.Fatal("QUERY description omitted required arguments", schema["description"])
	}
}

type openAPINullableArguments struct {
	Pointers  []*int32
	Optionals []serialization.Optional[int32]
	Key       int32
}

type openAPIArgumentRow struct{ ID int32 }

func TestOpenAPIQueryArgumentNullableElementsAndKelvinAlias(t *testing.T) {
	graph, err := contractGraph(t, `import "github.com/cratis/arc.go/serialization"
//arc:readmodel
type Row struct { ID int32 }
type Args struct { Pointers []*int32; Optionals []serialization.Optional[int32]; Key int32 }
//arc:query path=/api/nullable
func (Row) All(Args) ([]Row, error) { return nil, nil }
`, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	document, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	item := openAPIDecode(t, document.bytes())["paths"].(map[string]any)["/api/nullable"].(map[string]any)
	operation := item["x-cratis-query"].(map[string]any)["operation"].(map[string]any)
	schema := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	arguments := schema["properties"].(map[string]any)["arguments"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
	properties := arguments["properties"].(map[string]any)
	get := openAPIOperationParameters(t, item["get"].(map[string]any))
	for _, name := range []string{"pointers", "optionals"} {
		body := properties[name].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
		array := body["anyOf"].([]any)[0].(map[string]any)
		items := array["items"].(map[string]any)["anyOf"].([]any)
		if len(items) != 2 || items[1].(map[string]any)["type"] != "null" {
			t.Fatalf("%s JSON array items must accept null: %v", name, items)
		}
		forms := items[0].(map[string]any)["anyOf"].([]any)
		if forms[0].(map[string]any)["type"] != "integer" || forms[1].(map[string]any)["type"] != "string" {
			t.Fatalf("%s nonnull elements lost their integer/text forms: %v", name, forms)
		}
		if get[name]["schema"].(map[string]any)["items"].(map[string]any)["type"] != "integer" {
			t.Fatal("GET text elements must remain nonnullable", get[name])
		}
	}
	pattern := "^[kK\u212A][eE][yY]$"
	alias := arguments["patternProperties"].(map[string]any)[pattern]
	if !reflect.DeepEqual(alias, properties["key"]) || !regexp.MustCompile(pattern).MatchString("\u212Aey") {
		t.Fatal("Kelvin alias must use the declared key schema", arguments)
	}

	var registry queries.Registry
	var captured openAPINullableArguments
	if err := queries.Register[openAPIArgumentRow](&registry, "All", queries.Function(func(_ context.Context, args openAPINullableArguments) (openAPIArgumentRow, error) {
		captured = args
		return openAPIArgumentRow{}, nil
	}), queries.WithAuthorization[openAPINullableArguments](metadata.Authorization{AllowAnonymous: true})); err != nil {
		t.Fatal(err)
	}
	pipeline, err := registry.Build(queries.PipelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request, err := queries.ReadQUERY([]byte(`{"arguments":{"pointers":[null,1],"optionals":[null,"1"],"\u212Aey":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.Perform(t.Context(), "openAPIArgumentRow.All", request)
	if err != nil || !result.IsSuccess() {
		t.Fatal("nullable array elements did not bind", result, err)
	}
	if len(captured.Pointers) != 2 || captured.Pointers[0] != nil || captured.Pointers[1] == nil || *captured.Pointers[1] != 1 || len(captured.Optionals) != 2 || !captured.Optionals[0].IsNull() || captured.Key != 2 {
		t.Fatal("nullable elements or Kelvin argument lost their binding", captured)
	}
	if value, present := captured.Optionals[1].Value(); !present || value != 1 {
		t.Fatal("nonnull Optional element did not bind", value, present)
	}
	request, err = queries.ReadQUERY([]byte(`{"arguments":{"\u212Aey":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err = pipeline.Perform(t.Context(), "openAPIArgumentRow.All", request)
	var argumentError *queries.ArgumentError
	if !errors.As(err, &argumentError) || argumentError.Name != "key" || result.IsSuccess() {
		t.Fatal("Kelvin alias escaped the runtime integer constraint", result, err)
	}
	// The alias uses the same integer/text/null scalar alternatives as key,
	// none of which permit the object that the binder rejected.
	forms := alias.(map[string]any)["anyOf"].([]any)[0].(map[string]any)["anyOf"].([]any)
	if forms[0].(map[string]any)["type"] != "integer" || forms[1].(map[string]any)["type"] != "string" {
		t.Fatal("Kelvin alias lost its scalar constraint", alias)
	}
}

func TestOpenAPIQueryArgumentsAreOrderIndependent(t *testing.T) {
	graph := openAPIArgumentsFixture(t)
	first, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	reordered := openAPICloneGraph(t, graph)
	for i := range reordered.Queries {
		slices.Reverse(reordered.Queries[i].Parameters)
		slices.Reverse(reordered.Queries[i].SortFields)
	}
	slices.Reverse(reordered.Endpoints)
	second, err := renderOpenAPI(reordered)
	if err != nil || !bytes.Equal(first.bytes(), second.bytes()) {
		t.Fatal("argument or sort-field order changed the document", err)
	}
}

func TestOpenAPIQueryArgumentRefusals(t *testing.T) {
	for _, tc := range []struct{ name, source, message string }{
		{"model argument", "//arc:readmodel\ntype Row struct { ID string }\ntype Inner struct { A string }\ntype Args struct { Inner Inner }\nfunc (Row) All(Args) ([]Row, error) { return nil, nil }", "Inner"},
		{"presence preserving", "//arc:readmodel\ntype Row struct { ID string }\ntype Args struct { Name *string `query:\"preservePresence\"` }\nfunc (Row) All(Args) ([]Row, error) { return nil, nil }", "presence-preserving"},
		{"sorting without paging", "//arc:readmodel\ntype Row struct { ID string `sortable:\"true\"` }\nfunc (Row) All() ([]Row, error) { return nil, nil }", "without a pageable result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := contractGraph(t, tc.source, contractProfile(), false)
			if err != nil {
				if strings.Contains(err.Error(), tc.message) {
					return
				}
				t.Fatal(err)
			}
			document, err := renderOpenAPI(graph)
			if err == nil || !strings.Contains(err.Error(), tc.message) || len(document.bytes()) > 0 {
				t.Fatalf("error=%v bytes=%s; want %q", err, document.bytes(), tc.message)
			}
		})
	}
}

func TestOpenAPIQueryArgumentGraphRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		mutate        func(*FieldDescriptor)
	}{
		{"reserved control", "reserved GET control", func(f *FieldDescriptor) { f.Name = "PageSize" }},
		{"pattern-unsafe name", "ASCII identifier", func(f *FieldDescriptor) { f.Name = "na]me" }},
		{"missing binding", "builtin binding", func(f *FieldDescriptor) { f.Binding = nil }},
		{"unknown encoding", "binding encoding", func(f *FieldDescriptor) { f.Binding.Encoding = "json" }},
		{"unknown missing policy", "missing-value policy", func(f *FieldDescriptor) { f.Binding.Missing = "guess" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := openAPICloneGraph(t, openAPIArgumentsFixture(t))
			tc.mutate(&graph.Queries[0].Parameters[0])
			document, err := renderOpenAPI(graph)
			if err == nil || !strings.Contains(err.Error(), tc.message) || len(document.bytes()) > 0 {
				t.Fatalf("error=%v; want %q", err, tc.message)
			}
		})
	}
}

// Reader witnesses for the published GET controls and QUERY argument object,
// against the pinned runtime reader rather than the schema.
func TestOpenAPIQueryArgumentReaderWitnesses(t *testing.T) {
	get, err := queries.ReadGET(url.Values{"NAME": {"a"}, "tags": {"x, y"}, "pageSize": {"2"}, "page": {"bad"}, "sortBy": {"label"}, "sortDirection": {"DESC"}})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := get.Arguments().Get("name"); !ok || !reflect.DeepEqual(value, []string{"a"}) {
		t.Fatal("GET argument names are not case-insensitive", value)
	}
	parameters := get.Parameters()
	if !parameters.Paging.IsPaged || parameters.Paging.Size != 2 || parameters.Paging.Page != 0 || parameters.Sorting.Field != "label" {
		t.Fatal("GET paging/sorting controls differ from the published description", parameters)
	}
	if unpaged, err := queries.ReadGET(url.Values{"pageSize": {"two"}}); err != nil || unpaged.Parameters().Paging.IsPaged {
		t.Fatal("unparsable pageSize must leave the query unpaged", err)
	}
	if _, err := queries.ReadGET(url.Values{"sortBy": {"label"}, "sortDirection": {"sideways"}}); err == nil {
		t.Fatal("invalid sort direction was accepted")
	}
	if _, err := queries.ReadGET(url.Values{"page": {"1"}, "PAGE": {"2"}}); err == nil {
		t.Fatal("case-variant duplicate controls were accepted")
	}
	body, err := queries.ReadQUERY([]byte(`{"ARGUMENTS":{"Name":"a","limit":3,"tags":["x","y"]},"paging":{"page":1,"pageSize":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := body.Arguments().Get("name"); !ok || !body.Parameters().Paging.IsPaged {
		t.Fatal("QUERY arguments or paging were not read", body.Arguments().Entries())
	}
	if _, err := queries.ReadQUERY([]byte(`{"arguments":{"name":"a","NAME":"b"}}`)); err == nil {
		t.Fatal("duplicate case-variant argument names were accepted")
	}
}
