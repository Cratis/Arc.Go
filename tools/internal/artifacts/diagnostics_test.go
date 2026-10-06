// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goanalysis "golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

func TestAuthoringDiagnostics(t *testing.T) {
	loaded := loadDiagnosticFixtures(t, "", "./testdata/diagnostics/queries", "./testdata/diagnostics/negative", "./testdata/diagnostics/domain", "./testdata/diagnostics/invalidartifact", "./testdata/diagnostics/invalidconcept")
	if len(loaded) != 5 {
		t.Fatalf("fixture packages = %d, want 5", len(loaded))
	}
	for _, pkg := range loaded {
		t.Run(pkg.Name, func(t *testing.T) {
			syntaxBefore := diagnosticSyntax(t, pkg)
			findings, err := runDiagnosticFixture(pkg)
			switch pkg.Name {
			case "invalidartifact", "invalidconcept":
				want := "arc:query requires model="
				if pkg.Name == "invalidconcept" {
					want = "missing-codec"
				}
				if err == nil || !strings.Contains(err.Error(), want) || len(findings) != 0 {
					t.Fatalf("findings = %v, error = %v, want no partial report and %q", findings, err, want)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				wantCount := 0
				if pkg.Name == "queries" {
					wantCount = 6
					if pkg.Types.Scope().Lookup("RegisterArtifacts") == nil || pkg.Types.Scope().Lookup("ArcBindings") == nil {
						t.Fatal("generated symbols were removed from the package")
					}
				}
				assertDiagnosticLocations(t, pkg, findings, wantCount)
			}
			if after := diagnosticSyntax(t, pkg); !bytes.Equal(syntaxBefore, after) {
				t.Fatal("analyzer mutated input syntax")
			}
		})
	}
}

func TestAuthoringDiagnosticsRespectBuildTags(t *testing.T) {
	loaded := loadDiagnosticFixtures(t, "arcdiagnostics", "./testdata/diagnostics/queries")
	if len(loaded) != 1 {
		t.Fatalf("fixture packages = %d, want 1", len(loaded))
	}
	findings, err := runDiagnosticFixture(loaded[0])
	if err != nil {
		t.Fatal(err)
	}
	assertDiagnosticLocations(t, loaded[0], findings, 7)
}

func loadDiagnosticFixtures(t *testing.T, tags string, patterns ...string) []*packages.Package {
	t.Helper()
	config := &packages.Config{Context: t.Context(), Mode: packages.LoadSyntax}
	if tags != "" {
		config.BuildFlags = []string{"-tags=" + tags}
	}
	loaded, err := packages.Load(config, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(loaded) != 0 {
		t.Fatal("fixture type checking failed")
	}
	return loaded
}

func runDiagnosticFixture(pkg *packages.Package) ([]goanalysis.Diagnostic, error) {
	var findings []goanalysis.Diagnostic
	_, err := AuthoringAnalyzer.Run(&goanalysis.Pass{
		Analyzer: AuthoringAnalyzer, Fset: pkg.Fset, Files: pkg.Syntax,
		Pkg: pkg.Types, TypesInfo: pkg.TypesInfo, TypesSizes: pkg.TypesSizes,
		Report: func(finding goanalysis.Diagnostic) { findings = append(findings, finding) },
	})
	return findings, err
}

func diagnosticSyntax(t *testing.T, pkg *packages.Package) []byte {
	t.Helper()
	var data bytes.Buffer
	for _, file := range pkg.Syntax {
		if err := format.Node(&data, pkg.Fset, file); err != nil {
			t.Fatal(err)
		}
	}
	return data.Bytes()
}

func assertDiagnosticLocations(t *testing.T, pkg *packages.Package, findings []goanalysis.Diagnostic, wantCount int) {
	t.Helper()
	// Each marker names the exact field reference. This checks line, column,
	// span, and identity, not just a count or a diagnostic-message substring.
	expected := map[string]string{}
	for _, path := range pkg.CompiledGoFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for lineNumber, line := range strings.Split(string(data), "\n") {
			source, marker, marked := strings.Cut(line, "// want ARC0015 ")
			if !marked {
				continue
			}
			reference := strings.TrimSpace(marker)
			start := strings.Index(source, reference)
			if start < 0 {
				t.Fatalf("marker does not name a source reference: %s", line)
			}
			fieldStart := strings.LastIndex(reference, ".") + 1
			location := fmt.Sprintf("%s:%d:%d", path, lineNumber+1, start+fieldStart+1)
			expected[location] = reference[fieldStart:]
		}
	}
	if len(expected) != wantCount || len(findings) != wantCount {
		t.Fatalf("%s: markers = %d, findings = %d, want %d: %v", filepath.Base(pkg.PkgPath), len(expected), len(findings), wantCount, findings)
	}
	for _, finding := range findings {
		position := pkg.Fset.Position(finding.Pos)
		field, ok := expected[position.String()]
		if !ok || int(finding.End-finding.Pos) != len(field) || finding.Category != "ARC0015" || !strings.HasPrefix(finding.Message, "ARC0015: query argument ") {
			t.Fatalf("unexpected diagnostic at %s: %+v", position, finding)
		}
		delete(expected, position.String())
	}
	if len(expected) != 0 {
		t.Fatalf("missing diagnostics: %v", expected)
	}
}
