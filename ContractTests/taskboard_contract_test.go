// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cratis/arc.go/concepts"
)

// Adapted from Arc.Kotlin 23c93a3's selected Arc 22.14.0 contract. Expectations
// are independent literals, never generated with the Arc result constructors.
func exerciseTaskBoard(t *testing.T, origin string) {
	t.Helper()
	if err := validateOrigin(origin); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	const correlation = "00112233-4455-4677-8899-aabbccddeeff"
	send := func(t *testing.T, method, path, payload string) (int, map[string]any) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, origin+path, strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Correlation-ID", correlation)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1_000_001))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read/close: %v / %v", readErr, closeErr)
		}
		if len(body) > 1_000_000 {
			t.Fatal("oversized conformance response")
		}
		t.Logf("%s %s request=%q status=%d headers=%v body=%s", method, path, payload, response.StatusCode, response.Header, body)
		var decoded map[string]any
		if response.StatusCode != http.StatusNotFound {
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("response JSON: %v", err)
			}
			if got := response.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("content type = %q", got)
			}
			if got := response.Header.Get("X-Correlation-ID"); got != correlation || decoded["correlationId"] != got {
				t.Fatalf("correlation header/body mismatch: %q / %v", got, decoded["correlationId"])
			}
		}
		if method == "QUERY" && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("QUERY must be no-store")
		}
		return response.StatusCode, decoded
	}
	success := func(t *testing.T, method, path, payload string) map[string]any {
		t.Helper()
		status, body := send(t, method, path, payload)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		for name, want := range map[string]any{"isSuccess": true, "isAuthorized": true, "isValid": true, "hasExceptions": false, "validationResults": []any{}, "exceptionMessages": []any{}, "exceptionStackTrace": ""} {
			if !reflect.DeepEqual(body[name], want) {
				t.Fatalf("%s = %#v, want %#v", name, body[name], want)
			}
		}
		if method == "POST" {
			if body["authorizationFailureReason"] != "" {
				t.Fatal("successful command must have empty authorizationFailureReason")
			}
		} else {
			if body["isReady"] != true {
				t.Fatal("snapshot is not ready")
			}
			assertJSON(t, body["paging"], map[string]any{"page": 0, "size": 0, "totalItems": 0, "totalPages": 0})
		}
		return body
	}
	tasks := []map[string]any{}
	snapshot := func(t *testing.T, method string) {
		t.Helper()
		payload := ""
		if method == "QUERY" {
			payload = `{"arguments":{}}`
		}
		body := success(t, method, "/api/tasks", payload)
		rows, ok := body["data"].([]any)
		if !ok {
			t.Fatalf("data must be an array, got %#v", body["data"])
		}
		actual := make([]map[string]any, len(rows))
		for index, row := range rows {
			actual[index], ok = row.(map[string]any)
			if !ok {
				t.Fatalf("row is not an object: %#v", row)
			}
			if _, ok := actual[index]["id"].(string); !ok {
				t.Fatal("row ID is not a string")
			}
		}
		sort.Slice(actual, func(i, j int) bool { return actual[i]["id"].(string) < actual[j]["id"].(string) })
		want := append([]map[string]any{}, tasks...)
		sort.Slice(want, func(i, j int) bool { return want[i]["id"].(string) < want[j]["id"].(string) })
		assertJSON(t, actual, want)
	}
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"initial-empty-query", func(t *testing.T) { snapshot(t, "GET") }},
		{"typed-command-response", func(t *testing.T) {
			for _, title := range []string{"HTTP parity task", "Distinct second task"} {
				body := success(t, "POST", "/api/create-task", fmt.Sprintf(`{"title":%q}`, title))
				response, ok := body["response"].(map[string]any)
				if !ok {
					t.Fatal("missing typed command response")
				}
				id, ok := response["id"].(string)
				if !ok {
					t.Fatal("response ID is not a string")
				}
				uuid, err := concepts.ParseUUID(id)
				if err != nil || uuid.IsZero() || uuid.String() != id {
					t.Fatalf("invalid canonical UUID: %q", id)
				}
				assertJSON(t, response, map[string]any{"id": id, "title": title})
				tasks = append(tasks, map[string]any{"id": id, "title": title, "completed": false})
			}
			if tasks[0]["id"] == tasks[1]["id"] {
				t.Fatal("creates reused an identity")
			}
		}},
		{"get-query-arguments", func(t *testing.T) {
			for _, task := range tasks {
				body := success(t, "GET", "/api/tasks/by-id?id="+url.QueryEscape(task["id"].(string)), "")
				assertJSON(t, body["data"], task)
			}
		}},
		{"structured-query-arguments", func(t *testing.T) {
			for index := len(tasks) - 1; index >= 0; index-- {
				task := tasks[index]
				body := success(t, "QUERY", "/api/tasks/by-id", fmt.Sprintf(`{"arguments":{"id":%q}}`, task["id"]))
				assertJSON(t, body["data"], task)
			}
		}},
		{"enumerable-query-envelope", func(t *testing.T) { snapshot(t, "QUERY") }},
		{"validate-without-side-effects", func(t *testing.T) {
			body := success(t, "POST", "/api/create-task/validate", `{"title":"Validation does not execute"}`)
			if _, present := body["response"]; present {
				t.Fatal("validate must omit response")
			}
			snapshot(t, "GET")
		}},
		{"command-response-and-persisted-state", func(t *testing.T) {
			tasks[0]["completed"] = true
			body := success(t, "POST", "/api/complete-task", fmt.Sprintf(`{"taskId":%q}`, tasks[0]["id"]))
			assertJSON(t, body["response"], tasks[0])
			snapshot(t, "GET")
		}},
		{"malformed-input-no-side-effects", func(t *testing.T) {
			status, body := send(t, "POST", "/api/create-task", `{"title":`)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
			assertJSON(t, body, map[string]any{
				"correlationId": correlation, "isSuccess": false, "isAuthorized": true, "isValid": false, "hasExceptions": false,
				"validationResults": []any{map[string]any{"severity": 3, "message": "The request body could not be read or is not valid for this command.", "members": []any{}, "reason": "malformedRequest"}},
				"exceptionMessages": []any{}, "exceptionStackTrace": "", "authorizationFailureReason": "",
			})
			snapshot(t, "GET")
		}},
		{"unknown-route-status", func(t *testing.T) {
			status, _ := send(t, "GET", "/api/nonexistent-conformance-route", "")
			if status != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", status)
			}
		}},
	}
	if len(cases) != 9 {
		t.Fatalf("contract requires nine cases, got %d", len(cases))
	}
	executed := 0
	for _, tc := range cases {
		if !t.Run(tc.name, func(t *testing.T) { executed++; tc.run(t) }) {
			t.FailNow()
		}
	}
	if executed != 9 {
		t.Fatalf("vacuous conformance run: executed %d of 9 cases (check -run filter)", executed)
	}
}

func assertJSON(t *testing.T, got, want any) {
	t.Helper()
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err := json.Unmarshal(body, &normalized); err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(actual, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, normalized) {
		t.Fatalf("JSON = %s, want %s", actual, body)
	}
}

func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("conformance host must be an explicit loopback HTTP origin: %q", origin)
	}
	return nil
}
