//go:build ignore

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// Explicitly selected with the retained alternate tools manifest and an
// overlay that drops this file's ignore constraint; see
// testdata/openapi/README.md. Kin is a standard OpenAPI 3.1 consumer of the
// ordinary GET/HEAD operations only: it never sees QUERY as a method and its
// numeric validator is not used as exactness evidence.
func TestOpenAPIQueryArgumentsStandardConsumer(t *testing.T) {
	document, err := renderOpenAPI(openAPIArgumentsFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	original := document.bytes()
	loads := 0
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	loader.ReadFromURIFunc = openapi3.ReadFromURIFunc(func(_ *openapi3.Loader, _ *url.URL) ([]byte, error) {
		loads++
		return nil, fmt.Errorf("external load forbidden")
	})
	consumer, err := loader.LoadFromData(original)
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(consumer, openapi3.DisableSchemaDefaultsValidation(), openapi3.DisableExamplesValidation())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, target string
		valid          bool
	}{
		{"GET", "/api/rows?name=a&tags=x,y&flag=true&limit=3&page=1&pageSize=10&sortBy=label&sortDirection=desc", true},
		{"HEAD", "/api/rows?name=a", true},
		{"GET", "/api/rows", false},
		{"GET", "/api/rows?name=a&limit=many", false},
		{"GET", "/api/rows?name=a&flag=maybe", false},
	} {
		req, err := http.NewRequest(tc.method, tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		route, parameters, err := router.FindRoute(req)
		if err != nil || route.Method != tc.method {
			t.Fatalf("%s %s not routed: %v", tc.method, tc.target, err)
		}
		input := &openapi3filter.RequestValidationInput{Request: req, PathParams: parameters, Route: route, Options: &openapi3filter.Options{ExcludeRequestBody: true}}
		err = openapi3filter.ValidateRequest(context.Background(), input)
		if (err == nil) != tc.valid {
			t.Errorf("%s %s: %v; want valid=%t", tc.method, tc.target, err, tc.valid)
		}
	}
	req, err := http.NewRequest("QUERY", "/api/rows", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := router.FindRoute(req); err == nil {
		t.Fatal("standard consumer routed QUERY; the extension must stay opaque")
	}
	if loads != 0 || !bytes.Equal(original, document.bytes()) {
		t.Fatal("I/O or authoritative-byte mutation", loads)
	}
}
