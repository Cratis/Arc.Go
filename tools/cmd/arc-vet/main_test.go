// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArcVetCommandContracts(t *testing.T) {
	name := "arc-vet"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.CommandContext(t.Context(), "go", "build", "-mod=readonly", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build arc-vet: %v\n%s", err, output)
	}
	fixture := "../../internal/artifacts/testdata/diagnostics/"
	for _, test := range []struct {
		name     string
		args     []string
		tags     string
		wantExit int
		contains string
		findings int
	}{
		{name: "clean", args: []string{fixture + "negative"}},
		{name: "manual_registration", args: []string{fixture + "manualcommands"}},
		{name: "unselected_command", args: []string{fixture + "unselectedcommands"}},
		{name: "diagnostics", args: []string{fixture + "queries"}, wantExit: 3, contains: "ARC0015: query argument", findings: 6},
		{name: "build_tags", args: []string{fixture + "queries"}, tags: "arcdiagnostics", wantExit: 3, contains: "tagged.go:", findings: 7},
		{name: "invalid_concept", args: []string{fixture + "invalidconcept"}, wantExit: 1, contains: "missing-codec"},
		{name: "invalid_artifact", args: []string{fixture + "invalidartifact"}, wantExit: 1, contains: "arc:query requires model="},
		{name: "package_error", args: []string{fixture + "missing"}, wantExit: 1, contains: "missing"},
		{name: "imported_declarations", args: []string{fixture + "importeddeclarations"}, wantExit: 3, contains: "ARC0006: read-model dependency"},
		{name: "declaration_diagnostics", args: []string{fixture + "declarations"}, wantExit: 1, contains: "ARC0001: query must return"},
		{name: "tagged_declaration_diagnostics", args: []string{fixture + "declarations"}, tags: "arcdiagnostics", wantExit: 1, contains: "tagged.go:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), binary, test.args...)
			cmd.Env = append(os.Environ(), "GOFLAGS=-mod=readonly")
			if test.tags != "" {
				cmd.Env = append(cmd.Env, "GOFLAGS=-mod=readonly -tags="+test.tags)
			}
			output, err := cmd.CombinedOutput()
			exit := 0
			if err != nil {
				var failure *exec.ExitError
				if !errors.As(err, &failure) {
					t.Fatalf("execute arc-vet: %v", err)
				}
				exit = failure.ExitCode()
			}
			if exit != test.wantExit || !strings.Contains(string(output), test.contains) {
				t.Fatalf("exit = %d, want %d containing %q: %s", exit, test.wantExit, test.contains, output)
			}
			if count := strings.Count(string(output), "ARC0015: query argument"); count != test.findings {
				t.Fatalf("findings = %d, want %d: %s", count, test.findings, output)
			}
			if test.name == "imported_declarations" {
				if strings.Count(string(output), "ARC0003:") != 1 || strings.Count(string(output), "ARC0006:") != 1 {
					t.Fatalf("imported aliases lost declaration identity: %s", output)
				}
			}
			if test.name == "declaration_diagnostics" || test.name == "tagged_declaration_diagnostics" {
				for _, code := range []string{"ARC0001", "ARC0002", "ARC0003", "ARC0004", "ARC0005", "ARC0006", "ARC0014", "ARC0019"} {
					if !strings.Contains(string(output), code+":") {
						t.Fatalf("missing %s diagnostic: %s", code, output)
					}
				}
			}
			if test.wantExit == 0 && test.findings == 0 && len(output) != 0 {
				t.Fatalf("unexpected clean-package output: %s", output)
			}
		})
	}

	// Like go vet's analysis driver, -json reports diagnostics in JSON and
	// returns zero. Consumers must inspect records rather than treat zero as clean.
	t.Run("json", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), binary, "-json", fixture+"queries")
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=readonly")
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("arc-vet -json: %v", err)
		}
		type finding struct {
			Category string `json:"category"`
			Position string `json:"posn"`
			Message  string `json:"message"`
		}
		var report map[string]map[string][]finding
		if err := json.Unmarshal(output, &report); err != nil {
			t.Fatalf("decode diagnostics: %v: %s", err, output)
		}
		items := report["github.com/cratis/arc.go/tools/internal/artifacts/testdata/diagnostics/queries"]["arcauthoring"]
		if len(items) != 6 {
			t.Fatalf("JSON findings = %d, want 6: %s", len(items), output)
		}
		for _, item := range items {
			if item.Category != "ARC0015" || !strings.Contains(item.Position, "queries.go:") || !strings.HasPrefix(item.Message, "ARC0015:") {
				t.Fatalf("unexpected JSON diagnostic: %+v", item)
			}
		}
	})
}
