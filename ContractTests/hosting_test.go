// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/ContractTests/internal/taskboard"
	"github.com/cratis/arc.go/concepts"
)

func startHost(t *testing.T, a *arc.Application) {
	t.Helper()
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
}
func TestHostingFixtures(t *testing.T) {
	app, err := taskboard.New()
	if err != nil {
		t.Fatal(err)
	}
	startHost(t, app)
	data, err := os.ReadFile("fixtures/v1/hosting.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Method, Path, Body, Allow, Cache, Message string
		Status                                    int
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 6 {
		t.Fatal("vacuous hosting fixtures")
	}
	for _, f := range fixtures {
		t.Run(f.Method+f.Path, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest(f.Method, f.Path, strings.NewReader(f.Body)))
			if w.Code != f.Status || w.Header().Get("Allow") != f.Allow || w.Header().Get("Cache-Control") != f.Cache || !strings.Contains(w.Body.String(), f.Message) {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
			if f.Message == "" && w.Body.Len() != 0 {
				t.Fatal("nonempty routing/identity denial")
			}
		})
	}
}
func TestDiscoveryFixturesAreUnwrapped(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{Environment: "Development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	startHost(t, app)
	data, err := os.ReadFile("fixtures/v1/discovery.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Path string
		Body json.RawMessage
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 5 {
		t.Fatal("vacuous discovery fixtures")
	}
	for _, f := range fixtures {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", f.Path, nil))
		if w.Code != 200 || string(canonical(t, w.Body.Bytes())) != string(canonical(t, f.Body)) {
			t.Fatal(f.Path, w.Code, w.Body.String())
		}
	}
}
func TestNineTaskBoardConformanceCases(t *testing.T) {
	app, err := taskboard.New()
	if err != nil {
		t.Fatal(err)
	}
	startHost(t, app)
	send := func(method, path, body string) map[string]json.RawMessage {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Correlation-ID", "00112233-4455-4677-8899-aabbccddeeff")
		app.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if method == "QUERY" && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Header())
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"isSuccess", "isAuthorized", "isValid"} {
			if string(result[key]) != "true" {
				t.Fatal(key, w.Body.String())
			}
		}
		if string(result["correlationId"]) != `"00112233-4455-4677-8899-aabbccddeeff"` {
			t.Fatal(w.Body.String())
		}
		return result
	}
	initial := send("GET", "/api/tasks", "")
	if string(initial["data"]) != "[]" {
		t.Fatal(initial)
	}
	tasks := []taskboard.Task{}
	for _, title := range []string{"HTTP parity task", "Distinct second task"} {
		result := send("POST", "/api/create-task", `{"title":"`+title+`"}`)
		var created taskboard.Created
		if err := json.Unmarshal(result["response"], &created); err != nil {
			t.Fatal(err)
		}
		if created.ID.IsZero() || created.Title != title {
			t.Fatal(created)
		}
		tasks = append(tasks, taskboard.Task{ID: created.ID, Title: title})
	}
	if tasks[0].ID == tasks[1].ID {
		t.Fatal("identities not distinct")
	}
	for _, method := range []string{"GET", "QUERY"} {
		for _, task := range tasks {
			path := "/api/tasks/by-id"
			body := ""
			if method == "GET" {
				path += "?id=" + task.ID.String()
			} else {
				body = `{"arguments":{"id":"` + task.ID.String() + `"}}`
			}
			result := send(method, path, body)
			expected, err := json.Marshal(task)
			if err != nil {
				t.Fatal(err)
			}
			if string(canonical(t, result["data"])) != string(canonical(t, expected)) {
				t.Fatal(result)
			}
		}
	}
	snapshot := func(method string) {
		t.Helper()
		result := send(method, "/api/tasks", `{"arguments":{}}`)
		expected, err := json.Marshal(tasks)
		if err != nil {
			t.Fatal(err)
		}
		if string(canonical(t, result["data"])) != string(canonical(t, expected)) {
			t.Fatal(result)
		}
	}
	snapshot("QUERY")
	result := send("POST", "/api/create-task/validate", `{"title":"Must not execute"}`)
	if _, present := result["response"]; present {
		t.Fatal("validate returned response")
	}
	snapshot("GET")
	tasks[0].Completed = true
	result = send("POST", "/api/complete-task", `{"taskId":"`+tasks[0].ID.String()+`"}`)
	var completed taskboard.Task
	if err := json.Unmarshal(result["response"], &completed); err != nil {
		t.Fatal(err)
	}
	if completed != tasks[0] {
		t.Fatal(completed)
	}
	snapshot("GET")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("POST", "/api/create-task", strings.NewReader(`{"title":`)))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	snapshot("GET")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/api/nonexistent-conformance-route", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	if _, err := concepts.ParseUUID(tasks[0].ID.String()); err != nil {
		t.Fatal(err)
	}
}
