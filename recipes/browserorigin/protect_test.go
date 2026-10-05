// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package browserorigin_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/recipes/browserorigin"
	"github.com/cratis/arc.go/recipes/internal/fixture"
)

const (
	frontend = "https://app.example.com"
	attacker = "https://attacker.example"
)

func protectedServer(t *testing.T) (*fixture.Application, *httptest.Server) {
	t.Helper()
	f := fixture.New(t, arc.Options{})
	handler, err := browserorigin.Protect(f.App, frontend)
	if err != nil {
		t.Fatal(err)
	}
	return f, fixture.Serve(t, handler)
}

func browser(site, origin string, extra ...string) http.Header {
	header := http.Header{"Origin": {origin}}
	if site != "" {
		header.Set("Sec-Fetch-Site", site)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		header.Set(extra[i], extra[i+1])
	}
	return header
}

func varies(r fixture.Response, name string) bool {
	for _, value := range r.Header.Values("Vary") {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), name) {
				return true
			}
		}
	}
	return false
}

func TestNonBrowserCallersKeepArcBehavior(t *testing.T) {
	f, server := protectedServer(t)
	fixture.VerifyMount(t, f, server)
}

func TestTrustedOriginPreflightAllowsQuery(t *testing.T) {
	_, server := protectedServer(t)
	r := fixture.Do(t, server, http.MethodOptions, fixture.QueryPath, "", browser("cross-site", frontend,
		"Access-Control-Request-Method", "QUERY", "Access-Control-Request-Headers", "content-type"))
	methods := strings.Split(r.Header.Get("Access-Control-Allow-Methods"), ", ")
	if r.Status != http.StatusNoContent || r.Header.Get("Access-Control-Allow-Origin") != frontend ||
		!slices.Contains(methods, "QUERY") || !slices.Contains(methods, "POST") || !varies(r, "Origin") {
		t.Fatalf("got %d %v", r.Status, r.Header)
	}
}

func TestUntrustedOriginPreflightGrantsNothing(t *testing.T) {
	_, server := protectedServer(t)
	r := fixture.Do(t, server, http.MethodOptions, fixture.QueryPath, "", browser("cross-site", attacker,
		"Access-Control-Request-Method", "QUERY"))
	if r.Header.Get("Access-Control-Allow-Origin") != "" || r.Header.Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("got %d %v", r.Status, r.Header)
	}
}

func TestTrustedCrossOriginRequestsReachArc(t *testing.T) {
	f, server := protectedServer(t)
	before := f.Handled.Load()
	command := fixture.Do(t, server, http.MethodPost, fixture.CommandPath, `{"title":"cross"}`, browser("cross-site", frontend))
	query := fixture.Do(t, server, "QUERY", fixture.QueryPath, `{"arguments":{"title":"cross"}}`, browser("cross-site", frontend))
	for name, r := range map[string]fixture.Response{"command": command, "query": query} {
		if r.Status != http.StatusOK || r.Header.Get("Access-Control-Allow-Origin") != frontend ||
			r.Header.Get("Access-Control-Allow-Credentials") != "true" || !varies(r, "Origin") {
			t.Fatalf("%s: got %d %q %v", name, r.Status, r.Body, r.Header)
		}
	}
	if f.Handled.Load() != before+1 {
		t.Fatal("trusted command did not execute")
	}
}

func TestUntrustedCrossSiteUnsafeRequestsAreRejectedBeforeArc(t *testing.T) {
	f, server := protectedServer(t)
	before := f.Handled.Load()
	for name, header := range map[string]http.Header{
		"fetch metadata":                browser("cross-site", attacker),
		"origin without fetch metadata": browser("", attacker),
		"forged forwarded origin": browser("cross-site", attacker,
			"Forwarded", `host=app.example.com;proto=https`,
			"X-Forwarded-Host", "app.example.com", "X-Forwarded-Proto", "https"),
	} {
		t.Run(name, func(t *testing.T) {
			command := fixture.Do(t, server, http.MethodPost, fixture.CommandPath, `{"title":"forged"}`, header)
			query := fixture.Do(t, server, "QUERY", fixture.QueryPath, `{"arguments":{"title":"forged"}}`, header)
			for _, r := range []fixture.Response{command, query} {
				if r.Status != http.StatusForbidden || r.Header.Get("Access-Control-Allow-Origin") != "" {
					t.Fatalf("got %d %q %v", r.Status, r.Body, r.Header)
				}
			}
		})
	}
	if f.Handled.Load() != before {
		t.Fatal("forged command executed")
	}
}

func TestUntrustedSafeReadsAreServedButNotShared(t *testing.T) {
	_, server := protectedServer(t)
	r := fixture.Do(t, server, http.MethodGet, fixture.QueryPath+"?title=x", "", browser("cross-site", attacker))
	if r.Status != http.StatusOK || r.Header.Get("Access-Control-Allow-Origin") != "" || !varies(r, "Origin") {
		t.Fatalf("got %d %v", r.Status, r.Header)
	}
}

func TestSameOriginRequestsPass(t *testing.T) {
	f, server := protectedServer(t)
	before := f.Handled.Load()
	r := fixture.Do(t, server, http.MethodPost, fixture.CommandPath, `{"title":"same"}`, browser("same-origin", server.URL))
	if r.Status != http.StatusOK || f.Handled.Load() != before+1 {
		t.Fatalf("got %d %q", r.Status, r.Body)
	}
}

func TestInvalidTrustedOriginFailsConstruction(t *testing.T) {
	if _, err := browserorigin.Protect(http.NotFoundHandler(), "app.example.com/path"); err == nil {
		t.Fatal("expected an error for a malformed origin")
	}
}
