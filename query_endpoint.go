// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"net/http"
	"net/url"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/internal/httptransport"
	boundary "github.com/cratis/arc.go/internal/pipeline"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
)

type compiledReader struct {
	reader queries.RequestReader
	cache  string
}

func (a *Application) compileReaders() error {
	a.readers = map[string]compiledReader{"GET": {reader: queries.QueryStringRequestReader{}}, "QUERY": {reader: queries.BodyRequestReader{}, cache: "no-store"}}
	seen := map[string]bool{}
	for _, reader := range a.options.HTTP.QueryReaders {
		if nilValue(reader) {
			return ErrInvalidOptions
		}
		var method, cache string
		if err := boundary.Call(context.Background(), func(context.Context) error {
			method = reader.Method()
			cache = reader.ResponseCacheControl()
			return nil
		}); err != nil {
			return err
		}
		if method != "GET" && method != "QUERY" || seen[method] {
			return ErrInvalidOptions
		}
		seen[method] = true
		a.readers[method] = compiledReader{reader, cache}
	}
	return nil
}
func (a *Application) queryEndpoint(w http.ResponseWriter, r *http.Request, e metadata.Endpoint) {
	method := r.Method
	if method == "HEAD" {
		method = "GET"
	}
	reader := a.readers[method]
	if cache := reader.cache; cache != "" {
		w.Header().Set("Cache-Control", cache)
	}
	if method == "QUERY" {
		w.Header().Set("Cache-Control", "no-store")
	}
	id := correlation.FromContext(r.Context())
	var input queries.ReaderInput
	if method == "GET" {
		if len(r.URL.RawQuery) > a.options.HTTP.MaxQueryBytes {
			a.publish(w, r, 413, queries.FromError[any](id, &queries.ReadError{Malformed: true}))
			return
		}
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			a.publish(w, r, 400, queries.FromError[any](id, &queries.ReadError{Malformed: true, Cause: err}))
			return
		}
		input.Query = values
	} else {
		body, status, err := httptransport.ReadBody(w, r, a.options.HTTP.MaxBodyBytes)
		if err != nil {
			a.publish(w, r, status, queries.FromError[any](id, &queries.ReadError{Malformed: true, Cause: err}))
			return
		}
		input.Body = body
	}
	var request queries.Request
	err := boundary.Call(r.Context(), func(ctx context.Context) error {
		var err error
		request, err = reader.reader.Read(ctx, input)
		return err
	})
	if err != nil {
		a.publish(w, r, 400, queries.FromError[any](id, err))
		return
	}
	result, _ := a.queries.Perform(httpPipelineContext(r.Context()), queries.FullyQualifiedQueryName(e.Identity), request)
	a.publish(w, r, result.StatusCode(), result)
}
