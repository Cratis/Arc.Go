//go:build ignore

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Selected using the retained alternate tools manifest and the Stage 1 source
// overlay (package and embed paths only). No production validation dependency,
// schema reconstruction, kin numeric validation, or GET/POST substitution.
func TestOpenAPIQueryExactDocumentAndConsumer(t *testing.T) {
	document, err := renderOpenAPI(openAPIQueryFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	original := document.bytes()
	if path := os.Getenv("ARC_OPENAPI_QUERY_DOCUMENT"); path != "" {
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loads := 0
	structure, overlay, err := officialStructure(&loads)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := officialCheck(original, structure, overlay, &loads, true)
	if err != nil {
		t.Fatal(err)
	}
	const requestPointer = "/paths/~1api~1plain/x-cratis-query/operation/requestBody/content/application~1json/schema"
	request := checked.compiled[requestPointer]
	if request == nil || len(checked.inventory.operations) != 4 {
		t.Fatal("missing QUERY original-pointer schema or operations", checked.inventory)
	}
	decode := func(data string) any {
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{}`, true},
		{`{"arguments":null,"paging":null,"sorting":null}`, true},
		{`{"arguments":{"unused":{"nested":true}}}`, true},
		{`{"PAGING":{"PAGE":-2147483648,"PAGESIZE":2147483647}}`, true},
		{`{"pagİng":{"pageSİze":1}}`, true},
		{`{"pagİng":{"pageSİze":2147483648}}`, false},
		{`null`, false},
		{`[]`, false},
		{`{"arguments":[]}`, false},
		{`{"ARGUMENTS":[]}`, false},
		{`{"paging":{"pageSize":2147483648}}`, false},
		{`{"paging":{"pageSize":1.5}}`, false},
		{`{"paging":{"pageSize":null}}`, false},
		{`{"sorting":{"field":1}}`, false},
	} {
		if err := request.Validate(decode(tc.body)); (err == nil) != tc.valid {
			t.Errorf("exact QUERY request %s: %v; want valid=%t", tc.body, err, tc.valid)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"response status", func(op map[string]any) {
			op["responses"] = map[string]any{"wrong": map[string]any{"description": "wrong"}}
		}},
		{"unknown security", func(op map[string]any) { op["security"] = []any{map[string]any{"missing": []any{}}} }},
		{"duplicate ID", func(op map[string]any) { op["operationId"] = "Query.Shop.Row.Plain.GET" }},
		{"external schema", func(op map[string]any) {
			op["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "https://untrusted.invalid/schema"}}}}
		}},
		{"bad default", func(op map[string]any) {
			op["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "integer", "default": "wrong"}}}}
		}},
		{"unknown keyword", func(op map[string]any) {
			op["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "integer", "unknownAssertion": 1}}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := openAPIDecode(t, original)
			op := candidate["paths"].(map[string]any)["/api/plain"].(map[string]any)["x-cratis-query"].(map[string]any)["operation"].(map[string]any)
			tc.mutate(op)
			data, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := officialCheck(data, structure, overlay, &loads, true); err == nil {
				t.Fatal("invalid QUERY accepted")
			}
		})
	}
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
		method, path string
		found        bool
	}{
		{"GET", "/api/plain", true}, {"HEAD", "/api/plain", true},
		{"QUERY", "/api/plain", false}, {"QUERY", "/api/query-only", false},
		{"GET", "/api/query-only", false}, {"POST", "/api/query-only", false},
	} {
		// Incoming origin-form requests match this relative deployment-root
		// server. Kin's legacy router does not resolve an absolute URL against
		// a relative Server URL; no server or operation is rewritten here.
		req, err := http.NewRequest(tc.method, tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		route, _, err := router.FindRoute(req)
		if (err == nil) != tc.found || tc.found && route.Method != tc.method {
			t.Fatalf("%s %s => %v / %v", tc.method, tc.path, route, err)
		}
		t.Logf("kin v0.149.0 ordinary router: %s %s found=%t error=%v", tc.method, tc.path, err == nil, err)
	}
	if loads != 0 || !bytes.Equal(original, document.bytes()) {
		t.Fatal("I/O or authoritative-byte mutation", loads)
	}
	t.Logf("Stage 1 compiled %d original schema pointers; ordinary/QUERY operations=%d; external loads=%d", len(checked.compiled), len(checked.inventory.operations), loads)
}

// This uses the repository's existing generated-consumer pattern: a disposable
// consumer of the unchanged fetchable runtime pin, not a fifth tracked module.
func TestOpenAPIQueryGeneratedHTTPConsumer(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), `//arc:namespace Shop
package consumer
import "github.com/cratis/arc.go/commands"
//arc:command path=/api/save
type Save struct { Value int32 }
func (s Save) Handle() (commands.Outcome[int32], error) { return commands.Respond(s.Value), nil }
//arc:readmodel
type Row struct { Id int32; Label string }
//arc:query path=/api/plain
func (Row) Plain() ([]Row, error) { return []Row{{Id:3,Label:"row-three"}}, nil }
`)
	graph, err := buildGraph(graphPackages(t, dir, "."), contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	document, err := renderOpenAPI(graph)
	if err != nil {
		t.Fatal(err)
	}
	generate(t, Config{Dir: dir})
	put(t, filepath.Join(dir, "http_test.go"), `package consumer
import (
 "bytes"
 "context"
 "io"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "testing"
 arc "github.com/cratis/arc.go"
)
func TestGeneratedHTTP(t *testing.T) {
 builder, err := arc.NewBuilder(arc.Options{})
 if err != nil { t.Fatal(err) }
 if err := RegisterArtifacts(builder); err != nil { t.Fatal(err) }
 app, err := builder.Build()
 if err != nil { t.Fatal(err) }
 if err := app.Start(t.Context()); err != nil { t.Fatal(err) }
 t.Cleanup(func(){ if err := app.Shutdown(context.Background()); err != nil { t.Error(err) } })
 server := httptest.NewServer(app)
 defer server.Close()
 for _, tc := range []struct { name, method, path, body string; status int }{
  {"execute","POST","/api/save", "{\"value\":7}",200},
  {"validate","POST","/api/save/validate","{\"value\":7}",200},
  {"get","GET","/api/plain","",200},
  {"head","HEAD","/api/plain","",200},
  {"query","QUERY","/api/plain","{}",200},
  {"malformed","QUERY","/api/plain","{",400},
  {"null","QUERY","/api/plain","null",400},
  {"media","QUERY","/api/plain","{}",415},
 } {
  req, err := http.NewRequest(tc.method, server.URL+tc.path, bytes.NewBufferString(tc.body))
  if err != nil { t.Fatal(err) }
  req.Header.Set("Content-Type", "application/json")
  if tc.name == "media" { req.Header.Set("Content-Type", "text/plain") }
  response, err := server.Client().Do(req)
  if err != nil { t.Fatal(err) }
  data, err := io.ReadAll(response.Body)
  closeErr := response.Body.Close()
  if err != nil || closeErr != nil { t.Fatal(err,closeErr) }
  if response.StatusCode != tc.status { t.Fatalf("%s: %d %s",tc.name,response.StatusCode,data) }
  if tc.method == "QUERY" && response.Header.Get("Cache-Control") != "no-store" { t.Fatal("QUERY cache header",response.Header) }
  if err := os.WriteFile(filepath.Join(os.Getenv("ARC_OPENAPI_CAPTURE_DESTINATION"),tc.name+".json"),data,0600); err != nil { t.Fatal(err) }
  t.Logf("%s %s status=%d body=%s",tc.method,tc.path,response.StatusCode,data)
 }
}
`)
	captures := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "-run=^TestGeneratedHTTP$", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "ARC_OPENAPI_CAPTURE_DESTINATION="+captures)
	output, err := cmd.CombinedOutput()
	t.Logf("generated consumer: %s", output)
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	structure, overlay, err := officialStructure(&loads)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := officialCheck(document.bytes(), structure, overlay, &loads, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, component string }{
		{"execute", "Operation.Shop.Save.Execute"}, {"validate", "Operation.Shop.Save.Validate"},
		{"get", "Operation.Shop.Row.Plain.Query"}, {"query", "Operation.Shop.Row.Plain.Query"},
		{"malformed", "Operation.Shop.Row.Plain.Query"}, {"null", "Operation.Shop.Row.Plain.Query"}, {"media", "Operation.Shop.Row.Plain.Query"},
	} {
		data := get(t, filepath.Join(captures, tc.name+".json"))
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		schema := checked.compiled["/components/schemas/"+tc.component]
		if schema == nil {
			t.Fatal("missing component", tc.component)
		}
		if err := schema.Validate(instance); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if len(get(t, filepath.Join(captures, "head.json"))) != 0 || loads != 0 {
		t.Fatal("HEAD body or external loading")
	}
}

func TestOpenAPIQueryCapturedCSharpEnvelopes(t *testing.T) {
	dir := os.Getenv("ARC_OPENAPI_QUERY_CAPTURE")
	if dir == "" {
		t.Skip("requires retained pinned C# capture; source fixtures are not a substitute")
	}
	document, err := renderOpenAPI(openAPIQueryFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	structure, overlay, err := officialStructure(&loads)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := officialCheck(document.bytes(), structure, overlay, &loads, true)
	if err != nil {
		t.Fatal(err)
	}
	schema := checked.compiled["/components/schemas/Operation.Shop.Row.Plain.Query"]
	if schema == nil {
		t.Fatal("missing actual emitted response schema")
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		ArcRevision string
		SDK         string
		Cases       []struct {
			Name, BodyFile, BodySHA256 string
			Status                     int
		}
	}
	if err := json.Unmarshal(manifest, &capture); err != nil {
		t.Fatal(err)
	}
	if capture.ArcRevision != "7c1e78075b737df64f69fddfaae83374f75e3612" || capture.SDK != "10.0.401" || len(capture.Cases) != 11 {
		t.Fatal("wrong capture provenance/inventory", capture)
	}
	wantStatus := map[string]int{
		"plain-get": 200, "plain-head": 405, "query-empty": 200,
		"query-null-members": 200, "query-ignored-arguments": 200,
		"query-unpaged": 200, "query-null": 200, "query-malformed": 400,
		"query-page-overflow": 400, "query-sort-invalid": 400, "query-failure": 500,
	}
	seen := map[string]bool{}
	for _, entry := range capture.Cases {
		if seen[entry.Name] || wantStatus[entry.Name] != entry.Status {
			t.Fatal("unexpected/duplicate capture or status", entry)
		}
		seen[entry.Name] = true
		data, err := os.ReadFile(filepath.Join(dir, entry.BodyFile))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != entry.BodySHA256 {
			t.Fatal("capture hash mismatch", entry.Name)
		}
		if entry.Name == "plain-head" {
			if entry.Status != 405 || len(data) != 0 {
				t.Fatal("pinned C# HEAD deviation changed")
			}
			continue
		}
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(instance); err != nil {
			t.Fatalf("%s (%d): %v", entry.Name, entry.Status, err)
		}
		body := instance.(map[string]any)
		if body["isSuccess"] != (entry.Status == 200) {
			t.Fatal("capture success flag disagrees", entry.Name)
		}
		if entry.Status == 200 {
			rows, ok := body["data"].([]any)
			if !ok || len(rows) != 4 {
				t.Fatal("capture lost the four nonpageable rows", entry.Name)
			}
			for i, id := range []json.Number{"3", "1", "4", "2"} {
				if rows[i].(map[string]any)["id"] != id {
					t.Fatal("nonpageable capture reordered or paged rows", entry.Name)
				}
			}
		}
		t.Logf("captured C# %s status=%d validated against actual QUERY envelope", entry.Name, entry.Status)
	}
	if loads != 0 {
		t.Fatal("capture validation loaded externally")
	}
}
