//go:build integration

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/chronicle.go"
)

func TestDocumentedTaskboardCreatesAndQueriesTask(t *testing.T) {
	endpoint := os.Getenv("CHRONICLE_INTEGRATION_CONNECTION_STRING")
	if endpoint == "" {
		t.Fatal("CHRONICLE_INTEGRATION_CONNECTION_STRING is required")
	}
	id, err := concepts.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	app, adapter, err := createApplication(endpoint, chronicle.StoreName("arc-sample-"+id.String()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Shutdown(cleanup); err != nil {
			t.Error(err)
		}
		if err := adapter.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tasks/create", strings.NewReader(`{"title":"Write the chapter"}`)).WithContext(ctx))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var command struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &command); err != nil {
		t.Fatal(err)
	}
	if command.Response == "" {
		t.Fatal("missing created ID", response.Body.String())
	}
	query := httptest.NewRecorder()
	app.ServeHTTP(query, httptest.NewRequest(http.MethodGet, "/tasks/by-id?id="+command.Response, nil).WithContext(ctx))
	if query.Code != 200 {
		t.Fatal(query.Code, query.Body.String())
	}
	var result struct {
		Data Task `json:"data"`
	}
	if err := json.Unmarshal(query.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.ID != command.Response || result.Data.Title != "Write the chapter" {
		t.Fatal(result)
	}
}
