// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/validation"
)

type publicationModel struct {
	Payload any `json:"payload"`
}

type publicationProvider struct{}

type publicationEncoderError struct{ calls *atomic.Int32 }

func (v publicationEncoderError) MarshalJSON() ([]byte, error) {
	v.calls.Add(1)
	return nil, errors.New("SECRET encoder failure")
}

func TestQueryHTTPPublicationClearsPagingOnFailure(t *testing.T) {
	for _, mode := range []string{"success", "encoder-error", "response-size", "finding-state"} {
		t.Run(mode, func(t *testing.T) {
			var encoded, rendered atomic.Int32
			var payload any = "safe"
			switch mode {
			case "encoder-error":
				payload = publicationEncoderError{calls: &encoded}
			case "response-size":
				payload = strings.Repeat("SECRET", 1024)
			}
			b, err := arc.NewBuilder(arc.Options{HTTP: arc.HTTPOptions{MaxResponseBytes: 1024}})
			if err != nil {
				t.Fatal(err)
			}
			options := []queries.Option[queries.NoArguments]{
				queries.WithPath[queries.NoArguments]("/publish-query"),
				queries.WithRenderer[queries.NoArguments](func(context.Context, *execution.Scope) (queries.Renderer[publicationProvider, []publicationModel], error) {
					return queries.RendererFunc[publicationProvider, []publicationModel](func(context.Context, publicationProvider, queries.QueryContext) (queries.RendererResult[[]publicationModel], error) {
						rendered.Add(1)
						return queries.RendererResult[[]publicationModel]{Data: []publicationModel{{Payload: payload}}, TotalItems: 51}, nil
					}), nil
				}),
			}
			if mode == "finding-state" {
				options = append(options, queries.WithValidator(validation.ValidatorFunc[queries.NoArguments](func(context.Context, queries.NoArguments) ([]validation.Result, error) {
					return []validation.Result{{Severity: validation.Error, Message: "safe finding", State: make(chan int)}}, nil
				})))
			}
			if err := queries.Register[publicationModel](b, "All", queries.Function(func(context.Context, queries.NoArguments) (publicationProvider, error) {
				return publicationProvider{}, nil
			}), options...); err != nil {
				t.Fatal(err)
			}
			a, err := buildStarted(t, b)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(a)
			t.Cleanup(server.Close)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/publish-query?page=4&pageSize=10", nil)
			if err != nil {
				t.Fatal(err)
			}
			const correlationID = "00112233-4455-4677-8899-aabbccddeeff"
			req.Header.Set("X-Correlation-ID", correlationID)
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := response.Body.Close(); err != nil {
					t.Error(err)
				}
			}()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]any
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatalf("invalid failure envelope: %v, body %s", err, body)
			}
			if response.Header.Get("X-Correlation-ID") != correlationID || envelope["correlationId"] != correlationID || response.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("headers = %v, body = %s", response.Header, body)
			}
			wantPaging := map[string]any{"page": float64(0), "size": float64(0), "totalItems": float64(0), "totalPages": float64(0)}
			wantStatus, wantRendered := http.StatusInternalServerError, int32(1)
			if mode == "success" {
				wantStatus = http.StatusOK
				wantPaging = map[string]any{"page": float64(4), "size": float64(10), "totalItems": float64(51), "totalPages": float64(6)}
				if envelope["isSuccess"] != true || !reflect.DeepEqual(envelope["data"], []any{map[string]any{"payload": "safe"}}) {
					t.Fatalf("success changed: %s", body)
				}
			} else {
				if mode == "finding-state" {
					wantStatus, wantRendered = http.StatusBadRequest, 0
					findings, ok := envelope["validationResults"].([]any)
					if !ok || len(findings) != 1 || findings[0].(map[string]any)["message"] != "safe finding" || findings[0].(map[string]any)["state"] != nil {
						t.Fatalf("unsafe or lost finding: %s", body)
					}
				}
				_, data := envelope["data"]
				_, changes := envelope["changeSet"]
				wantMessages := []any{"An internal error occurred while processing the request. See server logs for details."}
				if data || changes || envelope["isSuccess"] != false || envelope["hasExceptions"] != true || envelope["exceptionStackTrace"] != "" || !reflect.DeepEqual(envelope["exceptionMessages"], wantMessages) || strings.Contains(string(body), "SECRET") {
					t.Fatalf("unsafe failure: %s", body)
				}
			}
			if response.StatusCode != wantStatus || rendered.Load() != wantRendered || !reflect.DeepEqual(envelope["paging"], wantPaging) {
				t.Fatalf("status = %d, rendered = %d, body = %s", response.StatusCode, rendered.Load(), body)
			}
			if mode == "encoder-error" && encoded.Load() != 1 {
				t.Fatalf("encoder called %d times; fallback must not retry unsafe data", encoded.Load())
			}
		})
	}
}
