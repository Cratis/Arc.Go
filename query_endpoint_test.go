package arc_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
	"github.com/cratis/fundamentals.go/dependencyinjection/container"
)

type parityHTTPArguments struct {
	Min int `json:"min"`
}

type parityHTTPBusiness struct{}

func TestQueryReaderParityFailureEnvelopes(t *testing.T) {
	const correlationID = "00112233-4455-4677-8899-aabbccddeeff"
	const redacted = "An internal error occurred while processing the request. See server logs for details."
	finding := func(message, member, reason string) map[string]any {
		return map[string]any{"severity": float64(3), "message": message, "members": []any{member}, "reason": reason}
	}
	for _, tc := range []struct {
		name, method, path, body string
		findings                 []any
		exception, denied        bool
	}{
		{"malformed-page", "QUERY", "/parity", `{"paging":{"page":"no","pageSize":2}}`, nil, true, false},
		{"malformed-size", "QUERY", "/parity", `{"paging":{"page":1,"pageSize":"no"}}`, nil, true, false},
		{"zero-size", "GET", "/parity?page=1&pageSize=0", "", []any{finding("Page size must be greater than 0", "Size", "rule")}, false, false},
		{"negative-size", "GET", "/parity?page=1&pageSize=-2", "", []any{finding("Page size must be greater than 0", "Size", "rule")}, false, false},
		{"sort-get", "GET", "/parity?sortby=name&sortDirection=no", "", []any{finding("The sort direction is not a recognized value.", "sortDirection", "malformedRequest")}, false, false},
		{"sort-query", "QUERY", "/parity", `{"sorting":{"field":"name","direction":"no"}}`, []any{finding("The sort direction is not a recognized value.", "sorting.direction", "malformedRequest")}, false, false},
		{"negative-page-get", "GET", "/parity?page=-1&pageSize=2", "", []any{finding("Page number must be greater than or equal to 0", "Page", "rule")}, false, false},
		{"negative-page-query", "QUERY", "/parity", `{"paging":{"page":-1,"pageSize":2}}`, []any{finding("Page number must be greater than or equal to 0", "Page", "rule")}, false, false},
		{"both-paging-findings", "GET", "/parity?page=-1&pageSize=0", "", []any{finding("Page number must be greater than or equal to 0", "Page", "rule"), finding("Page size must be greater than 0", "Size", "rule")}, false, false},
		{"binding-get", "GET", "/parity?min=no", "", []any{finding("The query argument is malformed.", "min", "malformedRequest")}, false, false},
		{"binding-query", "QUERY", "/parity", `{"arguments":{"min":"no"}}`, []any{finding("The query argument is malformed.", "min", "malformedRequest")}, false, false},
		{"denied-get", "GET", "/parity", "", nil, false, true},
		{"denied-query", "QUERY", "/parity", `{}`, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			performers, renderers, businessFactories := 0, 0, 0
			var registry container.Registry
			if err := di.Bind(&registry, di.Scoped, func(context.Context, di.Resolver) (*parityHTTPBusiness, error) {
				businessFactories++
				return &parityHTTPBusiness{}, nil
			}); err != nil {
				t.Fatal(err)
			}
			provider, err := registry.Build(container.WithContextGuard(execution.ContextGuard()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := provider.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			b, err := arc.NewBuilder(arc.Options{ScopeFactory: provider})
			if err != nil {
				t.Fatal(err)
			}
			declaration := metadata.Authorization{AllowAnonymous: !tc.denied}
			if err := queries.Register[builderModel](b, "Parity", queries.Invoke(func(ctx context.Context, inv *queries.Invocation, _ parityHTTPArguments) ([]builderModel, error) {
				performers++
				_, err := execution.Resolve[*parityHTTPBusiness](ctx, inv.Scope())
				return []builderModel{{Name: "must not publish"}}, err
			}), queries.WithPath[parityHTTPArguments]("/parity"), queries.WithAuthorization[parityHTTPArguments](declaration), queries.WithDependencies[parityHTTPArguments](di.KeyFor[*parityHTTPBusiness]()), queries.WithRenderer[parityHTTPArguments](func(context.Context, *execution.Scope) (queries.Renderer[[]builderModel, []builderModel], error) {
				renderers++
				return queries.RendererFunc[[]builderModel, []builderModel](func(context.Context, []builderModel, queries.QueryContext) (queries.RendererResult[[]builderModel], error) {
					t.Fatal("failure entered renderer")
					return queries.RendererResult[[]builderModel]{}, nil
				}), nil
			})); err != nil {
				t.Fatal(err)
			}
			app, err := buildStarted(t, b)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("X-Correlation-ID", correlationID)
			if tc.method == "QUERY" {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			status := 400
			if tc.denied {
				status = 403
			}
			cache := ""
			if tc.method == "QUERY" {
				cache = "no-store"
			}
			if w.Code != status || w.Header().Get("X-Correlation-ID") != correlationID || w.Header().Get("Cache-Control") != cache || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("status/headers = %d / %v", w.Code, w.Header())
			}
			findings := tc.findings
			if findings == nil {
				findings = []any{}
			}
			messages := []any{}
			if tc.exception {
				messages = append(messages, redacted)
			}
			want := map[string]any{
				"paging":        map[string]any{"page": float64(0), "size": float64(0), "totalItems": float64(0), "totalPages": float64(0)},
				"correlationId": correlationID, "isSuccess": false, "isReady": true,
				"isAuthorized": !tc.denied, "isValid": len(findings) == 0, "hasExceptions": tc.exception,
				"validationResults": findings, "exceptionMessages": messages, "exceptionStackTrace": "",
			}
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("envelope = %s; want %+v", w.Body.String(), want)
			}
			if performers != 0 || renderers != 0 || businessFactories != 0 {
				t.Fatalf("failure activated performer/renderer/business factory: %d/%d/%d", performers, renderers, businessFactories)
			}
		})
	}
}

type httpArguments struct {
	Name string `json:"name"`
}

func TestGETHEADQUERYReadersAndSnapshotNegotiation(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.Register[builderModel](b, "All", queries.Function(func(_ context.Context, a httpArguments) ([]builderModel, error) {
		return []builderModel{{Name: a.Name}}, nil
	}), queries.WithPath[httpArguments]("/items")); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/items?name=one", "", 200, `"name":"one"`}, {"QUERY", "/items", `{"arguments":{"name":"two","unknown":1}}`, 200, `"name":"two"`}, {"QUERY", "/items", `{`, 400, "An internal error occurred while processing the request. See server logs for details."}, {"GET", "/items?name=%zz", "", 400, "isSuccess"}, {"HEAD", "/items?name=one", "", 200, ""},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Accept", "text/event-stream")
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.method == "QUERY" && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Header())
			}
			if tc.method == "HEAD" && (w.Body.Len() != 0 || w.Header().Get("Content-Length") == "") {
				t.Fatal(w.Body.String(), w.Header())
			}
			if strings.Contains(w.Header().Get("Content-Type"), "event-stream") {
				t.Fatal("snapshot became stream")
			}
		})
	}
}
