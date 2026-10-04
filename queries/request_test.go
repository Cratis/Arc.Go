// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/validation"

	"github.com/cratis/arc.go/queries"
)

func TestGETControlsCaseFoldingNonpositivePagingAndWaitExclusion(t *testing.T) {
	request, err := queries.ReadGET(url.Values{"PAGESIZE": {"0"}, "PAGE": {"bad"}, "SORTBY": {"name"}, "SORTDIRECTION": {"DeSc"}, "waitForFirstResult": {"true"}, "WAITFORFIRSTRESULTTIMEOUT": {"30"}, "name": {"Ada"}})
	mustRegister(t, err)
	p := request.Parameters()
	if !p.Paging.IsPaged || p.Paging.Size != 0 || p.Paging.Page != 0 || p.Sorting.Direction != queries.Descending || p.Sorting.Field != "name" {
		t.Fatalf("parameters = %+v", p)
	}
	if len(request.Arguments().Entries()) != 1 {
		t.Fatalf("arguments = %+v", request.Arguments().Entries())
	}
	for _, values := range []url.Values{{"page": {"0"}, "PAGE": {"1"}}, {"pageSize": {"1", "2"}}, {"waitForFirstResult": {"1"}, "WAITFORFIRSTRESULT": {"2"}}} {
		if _, err := queries.ReadGET(values); !errors.Is(err, queries.ErrMalformedRequest) {
			t.Fatal(err)
		}
	}
	for _, size := range []string{"bad", "2147483648", ""} {
		request, err := queries.ReadGET(url.Values{"pageSize": {size}, "page": {"9"}})
		mustRegister(t, err)
		if request.Parameters().Paging.IsPaged {
			t.Fatal("unparseable size enabled paging")
		}
	}
	request, err = queries.ReadGET(url.Values{"pageSize": {"1"}, "page": {"2147483648"}, "sortby": {"name"}})
	mustRegister(t, err)
	if request.Parameters().Paging.Page != 0 || request.Parameters().Sorting.Direction != queries.Unspecified {
		t.Fatal("invalid page or incomplete sorting")
	}
}
func TestQUERYEnvelopeIntegerGrammarAndDirectionDefaults(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		malformed bool
		semantic  bool
		paged     bool
		direction queries.SortDirection
	}{
		{name: "unknown fields", body: `{"extension":{"anything":[]},"arguments":{"unknown":{"x":1}}}`},
		{name: "positive size", body: `{"paging":{"page":2,"pageSize":10}}`, paged: true},
		{name: "zero size", body: `{"paging":{"page":2,"pageSize":0}}`},
		{name: "negative size", body: `{"paging":{"pageSize":-1}}`},
		{name: "fraction", body: `{"paging":{"pageSize":1.5}}`, malformed: true},
		{name: "exponent", body: `{"paging":{"pageSize":1e2}}`, malformed: true},
		{name: "int overflow", body: `{"paging":{"pageSize":2147483648}}`, malformed: true},
		{name: "null integer", body: `{"paging":{"page":null}}`, malformed: true},
		{name: "root null", body: `null`, malformed: true},
		{name: "root array", body: `[]`, malformed: true},
		{name: "trailing", body: `{} {}`, malformed: true},
		{name: "duplicate envelope", body: `{"arguments":{},"Arguments":{}}`, malformed: true},
		{name: "duplicate argument", body: `{"arguments":{"name":1,"NAME":2}}`, malformed: true},
		{name: "direction omitted", body: `{"sorting":{"field":"name"}}`, direction: queries.Ascending},
		{name: "direction null", body: `{"sorting":{"field":"name","direction":null}}`, direction: queries.Ascending},
		{name: "empty direction", body: `{"sorting":{"field":"name","direction":""}}`, semantic: true},
		{name: "unknown direction", body: `{"sorting":{"field":"name","direction":"random"}}`, semantic: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := queries.ReadQUERY([]byte(tc.body))
			if tc.malformed || tc.semantic {
				var read *queries.ReadError
				if !errors.As(err, &read) || read.Malformed != tc.malformed {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Parameters().Paging.IsPaged != tc.paged || r.Parameters().Sorting.Direction != tc.direction {
				t.Fatalf("parameters = %+v", r.Parameters())
			}
		})
	}
	reader := queries.BodyRequestReader{}
	if reader.Method() != "QUERY" || reader.ResponseCacheControl() != "no-store" {
		t.Fatal("QUERY metadata changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Read(ctx, queries.ReaderInput{Body: []byte(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestMalformedQUERYIntegerRetainsOneActualDecoderCause(t *testing.T) {
	for _, body := range []string{`{"paging":{"page":"no","pageSize":2}}`, `{"paging":{"page":1,"pageSize":"no"}}`} {
		_, err := queries.ReadQUERY([]byte(body))
		var read *queries.ReadError
		var decoder *json.UnmarshalTypeError
		if !errors.Is(err, queries.ErrMalformedRequest) || !errors.As(err, &read) || !read.Malformed || !errors.As(err, &decoder) || errors.Unwrap(read) != decoder {
			t.Fatalf("decoder identity lost for %s: %#v", body, err)
		}
	}
	first, second := errors.New("first cause"), errors.New("second cause")
	joined := errors.Join(first, second)
	read := &queries.ReadError{Malformed: true, Cause: joined}
	if errors.Unwrap(read) != joined || !errors.Is(read, first) || !errors.Is(read, second) || !errors.Is(read, queries.ErrMalformedRequest) {
		t.Fatal("genuine joined causes were changed")
	}
	if errors.Is(&queries.ReadError{Cause: first}, queries.ErrMalformedRequest) {
		t.Fatal("semantic reader error acquired malformed category")
	}
}

func TestReaderDirectionFindingsAndGenericSortingRejections(t *testing.T) {
	for _, method := range []string{"GET", "QUERY"} {
		var err error
		member := "sortDirection"
		if method == "GET" {
			_, err = queries.ReadGET(url.Values{"sortby": {"name"}, "sortDirection": {"no"}})
		} else {
			member = "sorting.direction"
			_, err = queries.ReadQUERY([]byte(`{"sorting":{"field":"name","direction":"no"}}`))
		}
		var sorting *queries.SortingError
		var read *queries.ReadError
		if !errors.Is(err, queries.ErrInvalidSorting) || !errors.As(err, &sorting) || !errors.As(err, &read) || read.Malformed {
			t.Fatalf("%s sorting identities: %v", method, err)
		}
		want := []validation.Result{{Severity: validation.Error, Message: "The sort direction is not a recognized value.", Members: []string{member}, Reason: validation.MalformedRequest}}
		if !reflect.DeepEqual(sorting.ValidationResults(), want) {
			t.Fatalf("%s: %+v", method, sorting.ValidationResults())
		}
	}
	for _, sorting := range []*queries.SortingError{{Field: "sortby"}, {Field: "sorting.field", Cause: queries.ErrInvalidSorting}, {Field: "sortDirection"}} {
		if got := sorting.ValidationResults(); len(got) != 1 || got[0].Message != "The query sorting is invalid." {
			t.Fatalf("generic provider rejection changed: %+v", got)
		}
	}
}

func FuzzGETControls(f *testing.F) {
	f.Add("pageSize=10&page=0&sortby=name&sortDirection=desc")
	f.Add("PAGE=1&page=2")
	f.Add("name=")
	f.Fuzz(func(t *testing.T, s string) {
		values, err := url.ParseQuery(s)
		if err != nil {
			return
		}
		r, err := queries.ReadGET(values)
		if err != nil {
			return
		}
		for name := range r.Arguments().Entries() {
			switch name {
			case "page", "pageSize", "sortby", "sortDirection", "waitForFirstResult", "waitForFirstResultTimeout":
				t.Fatal("reserved argument leaked")
			}
		}
	})
}
func FuzzQUERYEnvelope(f *testing.F) {
	for _, seed := range []string{`{}`, `null`, `{"arguments":{"a":1}}`, `{"paging":{"pageSize":2147483648}}`, `{"arguments":{"a":1,"A":2}}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := queries.ReadQUERY([]byte(s))
		if err == nil && r.Parameters().Paging.IsPaged && r.Parameters().Paging.Size <= 0 {
			t.Fatal("QUERY enabled nonpositive paging")
		}
	})
}
