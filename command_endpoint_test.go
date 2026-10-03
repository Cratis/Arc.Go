package arc_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/tenancy"
	"github.com/cratis/arc.go/validation"
)

type httpCommand struct {
	Name string `json:"name" validate:"required"`
}

func TestCommandsExecuteValidateAndTransportFailures(t *testing.T) {
	calls := 0
	b, err := arc.NewBuilder(arc.Options{HTTP: arc.HTTPOptions{MaxBodyBytes: 32}})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[httpCommand](b, commands.Invoke(func(ctx context.Context, _ *commands.Invocation, c httpCommand) (int, error) {
		calls++
		tenant, _ := tenancy.TenantFrom(ctx)
		if tenant.String() != "team" {
			t.Fatal(tenant)
		}
		if c.Name == "fail" {
			return 0, errors.New("SECRET")
		}
		return 0, nil
	}), commands.WithPath[httpCommand]("/add")); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, body, media, severity string
		status, delta               int
		contains                    string
	}{
		{"/add", `{"name":"ok"}`, "", "", 200, 1, `"response":0`},
		{"/add/validate", `{"name":"ok"}`, "", "", 200, 0, `"isSuccess":true`},
		{"/add", `{}`, "", "", 400, 0, `"isValid":false`},
		{"/add", `null`, "", "", 400, 0, "The request body could not be read or is not valid for this command."},
		{"/add", `{"name":"ok"} {}`, "", "", 400, 0, "malformedRequest"},
		{"/add", `{"name":"ok"}`, "text/plain", "", 415, 0, "malformedRequest"},
		{"/add", `{"name":"` + strings.Repeat("x", 40) + `"}`, "", "", 413, 0, "malformedRequest"},
		{"/add", `{"name":"fail"}`, "application/json; charset=utf-8", "", 500, 1, "An internal error occurred while processing the request. See server logs for details."},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body+tc.media, func(t *testing.T) {
			before := calls
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.media)
			r.Header.Set("x-cratis-tenant-id", "team")
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != tc.status || calls-before != tc.delta || !strings.Contains(w.Body.String(), tc.contains) || strings.Contains(w.Body.String(), "SECRET") {
				t.Fatal(w.Code, calls-before, w.Body.String())
			}
		})
	}
}
func TestAllowedSeverityHeaderFiltering(t *testing.T) {
	b, err := arc.NewBuilder(arc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.Register[httpCommand](b, commands.Invoke(func(context.Context, *commands.Invocation, httpCommand) (int, error) { return 1, nil }), commands.WithPath[httpCommand]("/severity"), commands.WithValidator[httpCommand](validation.ValidatorFunc[httpCommand](func(context.Context, httpCommand) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Warning, Message: "warning"}}, nil
	}))); err != nil {
		t.Fatal(err)
	}
	a, err := buildStarted(t, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		values []string
		status int
	}{{nil, 200}, {[]string{"3"}, 200}, {[]string{"2"}, 200}, {[]string{"1"}, 400}, {[]string{"2", "2"}, 200}, {[]string{"2,2"}, 200}} {
		r := httptest.NewRequest("POST", "/severity", strings.NewReader(`{"name":"ok"}`))
		r.Header["X-Allowed-Severity"] = tc.values
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}
