// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cratis/arc.go/queries"
)

// Shared raw bodies are exercised by both the pinned runtime reader and the
// exact emitted request schema. Semantic direction errors are not type errors.
type openAPIQuerySortingWitness struct {
	name, body string
	malformed  bool
	invalid    bool
	sorting    queries.Sorting
}

func openAPIQuerySortingWitnesses() []openAPIQuerySortingWitness {
	var cases []openAPIQuerySortingWitness
	for _, field := range []struct{ name, member string }{
		{"absent", ""}, {"empty", `"field":"",`}, {"null", `"field":null,`},
	} {
		for _, direction := range []string{`42`, `[]`, `{}`, `true`, `null`, `"sideways"`, `"DeSc"`} {
			cases = append(cases, openAPIQuerySortingWitness{
				name: field.name + " field ignores " + direction,
				body: `{"sorting":{` + field.member + `"direction":` + direction + `}}`,
			})
		}
	}
	for _, direction := range []string{`42`, `[]`, `{}`, `true`} {
		cases = append(cases, openAPIQuerySortingWitness{
			name: "nonempty field rejects " + direction,
			body: `{"sorting":{"field":"id","direction":` + direction + `}}`, malformed: true,
		})
	}
	for _, field := range []string{`42`, `[]`, `{}`, `true`} {
		cases = append(cases, openAPIQuerySortingWitness{
			name: "nonstring field " + field,
			body: `{"sorting":{"field":` + field + `,"direction":[]}}`, malformed: true,
		})
	}
	cases = append(cases,
		openAPIQuerySortingWitness{name: "missing direction defaults", body: `{"sorting":{"field":"id"}}`, sorting: queries.Sorting{Field: "id", Direction: queries.Ascending}},
		openAPIQuerySortingWitness{name: "null direction defaults", body: `{"sorting":{"field":"id","direction":null}}`, sorting: queries.Sorting{Field: "id", Direction: queries.Ascending}},
		openAPIQuerySortingWitness{name: "invalid direction is semantic", body: `{"sorting":{"field":"id","direction":"sideways"}}`, invalid: true},
		openAPIQuerySortingWitness{name: "empty direction is semantic", body: `{"sorting":{"field":"id","direction":""}}`, invalid: true},
		openAPIQuerySortingWitness{name: "mixed case ignores direction", body: `{"SoRtİnG":{"FiElD":"","DiReCtİoN":[]}}`},
		openAPIQuerySortingWitness{name: "mixed case constrains direction", body: `{"SoRtİnG":{"FiElD":"id","DiReCtİoN":[]}}`, malformed: true},
		openAPIQuerySortingWitness{name: "mixed case constrains field", body: `{"SoRtİnG":{"FiElD":42,"DiReCtİoN":"asc"}}`, malformed: true},
		openAPIQuerySortingWitness{name: "mixed case valid direction", body: `{"SoRtİnG":{"FiElD":"id","DiReCtİoN":"DeSc"}}`, sorting: queries.Sorting{Field: "id", Direction: queries.Descending}},
		openAPIQuerySortingWitness{name: "mixed case invalid direction", body: `{"SORTING":{"FIELD":"id","DIRECTION":"sideways"}}`, invalid: true},
		openAPIQuerySortingWitness{name: "unknown field does not activate sorting", body: `{"sorting":{"fields":"id","direction":[]}}`},
		openAPIQuerySortingWitness{name: "whitespace field activates sorting", body: `{"sorting":{"field":" ","direction":[]}}`, malformed: true},
	)
	for _, direction := range []struct {
		name string
		want queries.SortDirection
	}{
		{"aSc", queries.Ascending}, {"AsCeNdİnG", queries.Ascending},
		{"dEsC", queries.Descending}, {"DeScEnDİnG", queries.Descending},
	} {
		cases = append(cases, openAPIQuerySortingWitness{
			name:    "valid " + direction.name,
			body:    fmt.Sprintf(`{"sorting":{"field":"id","direction":%q}}`, direction.name),
			sorting: queries.Sorting{Field: "id", Direction: direction.want},
		})
	}
	return cases
}

func TestOpenAPIQuerySortingReaderWitnesses(t *testing.T) {
	for _, tc := range openAPIQuerySortingWitnesses() {
		t.Run(tc.name, func(t *testing.T) {
			request, err := queries.ReadQUERY([]byte(tc.body))
			if tc.malformed || tc.invalid {
				var readError *queries.ReadError
				if !errors.As(err, &readError) || readError.Malformed != tc.malformed {
					t.Fatalf("ReadQUERY(%s): %v; want malformed=%t", tc.body, err, tc.malformed)
				}
				if tc.invalid && !errors.Is(err, queries.ErrInvalidSorting) {
					t.Fatalf("want semantic direction error, got %v", err)
				}
				return
			}
			if err != nil || request.Parameters().Sorting != tc.sorting {
				t.Fatalf("ReadQUERY(%s): sorting=%+v, error=%v; want %+v", tc.body, request.Parameters().Sorting, err, tc.sorting)
			}
		})
	}
}

func TestOpenAPIQueryRawParserWitnesses(t *testing.T) {
	for _, body := range []string{
		`{"sorting":{"direction":42,"DIRECTION":[]}}`,
		`{"sorting":{"field":"","FIELD":null}}`,
		`{"sorting":{"field":null,"FİELD":"id"}}`,
		`{"sorting":{},"SORTİNG":{}}`,
		`{"sorting":{"direction":[]}} {}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, err := queries.ReadQUERY([]byte(body))
			var readError *queries.ReadError
			if !errors.As(err, &readError) || !readError.Malformed {
				t.Fatalf("raw duplicate/trailing JSON must be malformed: %v", err)
			}
		})
	}
}

func openAPIQueryIntegerWitnesses() []struct {
	body  string
	valid bool
} {
	var cases []struct {
		body  string
		valid bool
	}
	for _, member := range []string{"page", "pageSize"} {
		for _, token := range []string{"1", "1.0", "1e0", "1E+0"} {
			cases = append(cases, struct {
				body  string
				valid bool
			}{fmt.Sprintf(`{"paging":{%q:%s}}`, member, token), token == "1"})
		}
	}
	return cases
}

func TestOpenAPIQueryIntegerTokenReaderWitnesses(t *testing.T) {
	for _, tc := range openAPIQueryIntegerWitnesses() {
		t.Run(tc.body, func(t *testing.T) {
			_, err := queries.ReadQUERY([]byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("ReadQUERY(%s): %v; want valid=%t", tc.body, err, tc.valid)
			}
			if !tc.valid {
				var readError *queries.ReadError
				if !errors.As(err, &readError) || !readError.Malformed {
					t.Fatalf("integer lexical failure must be malformed: %v", err)
				}
			}
		})
	}
}
