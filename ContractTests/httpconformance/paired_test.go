//go:build httpconformance

// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPairedSnapshotHTTP(t *testing.T) {
	cases := corpus()
	if err := validateCorpus(cases); err != nil {
		t.Fatal(err)
	}
	dll := os.Getenv("ARC_HTTP_CONFORMANCE_DLL")
	if !filepath.IsAbs(dll) {
		t.Fatal("ARC_HTTP_CONFORMANCE_DLL must identify the built exact-source reference DLL")
	}
	if info, err := os.Stat(dll); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("missing built C# host: %s: %v", dll, err)
	}
	if err := verifyProvenance(os.Getenv("ARC_HTTP_CONFORMANCE_PROVENANCE"), dll); err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("ARC_HTTP_CONFORMANCE_OUTPUT")
	if !filepath.IsAbs(output) {
		t.Fatal("ARC_HTTP_CONFORMANCE_OUTPUT must identify task-owned raw exchange output")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	csharp := startWithActivation(t, "dotnet", []string{dll}, nil, true)
	command, args, env := child(t, "go")
	goHost := start(t, command, args, env)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	executed := 0
	for _, c := range cases {
		t.Run(c.Group+"/"+c.Name, func(t *testing.T) {
			executed++
			left, leftErr := request(t.Context(), client, csharp, c)
			right, rightErr := request(t.Context(), client, goHost, c) // Always attempt both, even if one fails.
			differences := compare(left, right)
			record := struct {
				Request              requestCase `json:"request"`
				Csharp, Go           exchange
				Differences          []string
				CsharpError, GoError string
			}{Request: c, Csharp: left, Go: right, Differences: differences}
			if leftErr != nil {
				record.CsharpError = leftErr.Error()
			}
			if rightErr != nil {
				record.GoError = rightErr.Error()
			}
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			name := strings.ReplaceAll(c.Group+"-"+c.Name, "/", "-") + ".json"
			if err := os.WriteFile(filepath.Join(output, name), append(data, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			if leftErr != nil || rightErr != nil {
				t.Fatalf("exchange failed: C# %v; Go %v", leftErr, rightErr)
			}
			for name, e := range map[string]exchange{"C#": left, "Go": right} {
				if err := checkEnvelope(e); err != nil {
					t.Errorf("%s envelope: %v", name, err)
				}
			}
			if len(differences) > 0 {
				t.Errorf("unaccepted parity differences (no wildcard exemptions):\n%s", strings.Join(differences, "\n"))
			}
		})
	}
	if executed != caseCount {
		t.Fatalf("partial execution: ran %d of %d required cases", executed, caseCount)
	}
}

// TestReferenceActivation proves the actual generated host inventory before any HTTP exchange.
// Negative witnesses use the same host and readiness verifier, not replacement handlers.
func TestReferenceActivation(t *testing.T) {
	dll := os.Getenv("ARC_HTTP_CONFORMANCE_DLL")
	if !filepath.IsAbs(dll) {
		t.Fatal("ARC_HTTP_CONFORMANCE_DLL must identify the built exact-source reference DLL")
	}
	if err := verifyProvenance(os.Getenv("ARC_HTTP_CONFORMANCE_PROVENANCE"), dll); err != nil {
		t.Fatal(err)
	}
	t.Run("complete", func(t *testing.T) {
		startWithActivation(t, "dotnet", []string{dll}, nil, true)
	})
	for _, witness := range []struct{ name, diagnostic string }{
		{"missing-performer", "expected 4 Row performers; got 3"},
		{"missing-query", "expected 8 Row endpoints; got 7"},
	} {
		t.Run(witness.name, func(t *testing.T) {
			h, err := launch(context.WithoutCancel(t.Context()), "dotnet", []string{dll, "--readiness-negative", witness.name}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup := h.stop(5 * time.Second)
				retainHost(t, h, cleanup)
				var exit *exec.ExitError
				if !errors.As(cleanup, &exit) || exit.ExitCode() != 1 {
					t.Errorf("negative fixture must exit 1 and join: %v", cleanup)
				}
				select {
				case <-h.done:
				default:
					t.Error("negative fixture not joined")
				}
				if cleanup != nil && strings.Contains(cleanup.Error(), "forced cleanup") {
					t.Error(cleanup)
				}
				// Inspect the complete joined output, not just the first diagnostic line.
				log := h.output.String()
				if strings.Contains(log, "httpconformance-ready") || !strings.Contains(log, witness.diagnostic) {
					t.Errorf("negative witness did not fail before advertisement with expected diagnostic:\n%s", log)
				}
			})
			if _, err := h.originWithActivation(t.Context(), true); err == nil {
				t.Fatal("incomplete activation advertised readiness")
			}
		})
	}
}

func request(ctx context.Context, client *http.Client, origin string, c requestCase) (exchange, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, c.Method, origin+c.Path, strings.NewReader(c.Body))
	if err != nil {
		return exchange{}, err
	}
	req.Header.Set("X-Correlation-ID", correlationID)
	req.Header.Set("Accept", "application/json")
	if c.Method == "QUERY" {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return exchange{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	closeErr := response.Body.Close()
	e := exchange{Status: response.StatusCode, Header: response.Header.Clone(), Body: body}
	if len(body) > 1024*1024 {
		e.Body = body[:1024*1024]
		return e, fmt.Errorf("response exceeds 1 MiB")
	}
	return e, errors.Join(readErr, closeErr)
}
