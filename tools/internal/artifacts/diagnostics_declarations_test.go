// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	goanalysis "golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

func TestDeclarationDiagnostics(t *testing.T) {
	for _, tags := range []string{"", "arcdiagnostics"} {
		t.Run("tags="+tags, func(t *testing.T) {
			loaded := loadDiagnosticFixtures(t, tags, "./testdata/diagnostics/declarations")
			if len(loaded) != 1 {
				t.Fatalf("fixture packages = %d, want 1", len(loaded))
			}
			pkg := loaded[0]
			before := diagnosticSyntax(t, pkg)
			var findings []goanalysis.Diagnostic
			_, err := DeclarationAnalyzer.Run(&goanalysis.Pass{
				Analyzer: DeclarationAnalyzer, Fset: pkg.Fset, Files: pkg.Syntax,
				Pkg: pkg.Types, TypesInfo: pkg.TypesInfo, TypesSizes: pkg.TypesSizes,
				Report: func(finding goanalysis.Diagnostic) { findings = append(findings, finding) },
			})
			if err != nil {
				t.Fatal(err)
			}
			assertDeclarationDiagnostics(t, pkg, findings, tags != "")
			if !bytes.Equal(before, diagnosticSyntax(t, pkg)) {
				t.Fatal("analyzer mutated syntax")
			}
		})
	}
}

func TestDeclarationDiagnosticsAllowManualCommands(t *testing.T) {
	loaded := loadDiagnosticFixtures(t, "", "./testdata/diagnostics/manualcommands", "./testdata/diagnostics/unselectedcommands")
	if len(loaded) != 2 {
		t.Fatalf("fixture packages = %d, want 2", len(loaded))
	}
	for _, pkg := range loaded {
		t.Run(pkg.Name, func(t *testing.T) {
			var findings []goanalysis.Diagnostic
			_, err := DeclarationAnalyzer.Run(&goanalysis.Pass{
				Analyzer: DeclarationAnalyzer, Fset: pkg.Fset, Files: pkg.Syntax,
				Pkg: pkg.Types, TypesInfo: pkg.TypesInfo, TypesSizes: pkg.TypesSizes,
				Report: func(finding goanalysis.Diagnostic) { findings = append(findings, finding) },
			})
			if err != nil || len(findings) != 0 {
				t.Fatalf("manual command findings = %+v, error = %v", findings, err)
			}
		})
	}
}

func assertDeclarationDiagnostics(t *testing.T, pkg *packages.Package, findings []goanalysis.Diagnostic, tagged bool) {
	t.Helper()
	type expectedDiagnostic struct{ code, reference string }
	expected := map[string]expectedDiagnostic{}
	counts := map[string]int{}
	for _, path := range pkg.CompiledGoFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			source, marker, found := strings.Cut(line, "// want ")
			if !found {
				continue
			}
			code, reference, found := strings.Cut(marker, " ")
			span := regexp.MustCompile(`\b` + regexp.QuoteMeta(reference) + `\b`).FindStringIndex(source)
			if !found || span == nil {
				t.Fatalf("invalid marker %q", line)
			}
			location := fmt.Sprintf("%s:%d:%d", path, number+1, span[0]+1)
			expected[location] = expectedDiagnostic{code, reference}
			counts[code]++
		}
	}
	wantCounts := map[string]int{"ARC0001": 12, "ARC0002": 3, "ARC0003": 1, "ARC0004": 2, "ARC0005": 3, "ARC0006": 3, "ARC0014": 3, "ARC0019": 2}
	for _, path := range pkg.CompiledGoFiles {
		if filepath.Base(path) == "generic_methods.go" {
			wantCounts["ARC0014"]++
		}
	}
	for code, want := range wantCounts {
		if tagged {
			want++
		}
		if counts[code] != want {
			t.Fatalf("%s markers = %d, want %d (fixture coverage disappeared)", code, counts[code], want)
		}
	}
	if len(findings) != len(expected) {
		t.Fatalf("findings = %d, markers = %d: %+v", len(findings), len(expected), findings)
	}
	for _, finding := range findings {
		position := pkg.Fset.Position(finding.Pos).String()
		want, exists := expected[position]
		if !exists || finding.Category != want.code || !strings.HasPrefix(finding.Message, want.code+": ") || int(finding.End-finding.Pos) != len(want.reference) {
			t.Fatalf("unexpected finding at %s: %+v, want %+v", position, finding, want)
		}
		delete(expected, position)
	}
	if len(expected) != 0 {
		t.Fatalf("missing diagnostics: %v", expected)
	}
}
