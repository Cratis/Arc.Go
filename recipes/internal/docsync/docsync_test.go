// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package docsync_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	moduleRoot = "../.."
	docsRoot   = "../../../Documentation/backend/go/recipes"
)

var (
	regionStart = regexp.MustCompile(`^\s*// recipe:start ([a-z0-9-]+)\s*$`)
	regionEnd   = regexp.MustCompile(`^\s*// recipe:end\s*$`)
)

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}

// dedent trims surrounding blank lines and the common tab indentation, then
// renders the remaining indentation as four spaces per tab, as the
// documentation does.
func dedent(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if n := len(line) - len(strings.TrimLeft(line, "\t")); indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= indent && indent > 0 {
			line = line[indent:]
		}
		trimmed := strings.TrimLeft(line, "\t")
		out[i] = strings.Repeat("    ", len(line)-len(trimmed)) + trimmed
	}
	return strings.Join(out, "\n")
}

func regions(t *testing.T) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		var name string
		var body []string
		for number, line := range strings.Split(read(t, path), "\n") {
			switch {
			case regionStart.MatchString(line):
				if name != "" {
					t.Fatalf("%s:%d: nested recipe region", path, number+1)
				}
				name, body = regionStart.FindStringSubmatch(line)[1], nil
				if _, duplicate := found[name]; duplicate {
					t.Fatalf("%s:%d: duplicate recipe region %q", path, number+1, name)
				}
			case regionEnd.MatchString(line):
				if name == "" {
					t.Fatalf("%s:%d: recipe:end without start", path, number+1)
				}
				found[name], name = dedent(body), ""
			case name != "":
				body = append(body, line)
			}
		}
		if name != "" {
			t.Fatalf("%s: unterminated recipe region %q", path, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func goBlocks(content string) []string {
	var blocks []string
	var current []string
	inside := false
	for _, line := range strings.Split(content, "\n") {
		switch {
		case !inside && strings.TrimSpace(line) == "```go":
			inside, current = true, nil
		case inside && strings.TrimSpace(line) == "```":
			inside = false
			blocks = append(blocks, dedent(current))
		case inside:
			current = append(current, line)
		}
	}
	return blocks
}

func TestHostingDocumentsTheTestedConcurrentShutdown(t *testing.T) {
	sources := regions(t)
	shutdown, ok := sources["host-shutdown"]
	if !ok {
		t.Fatal("no tested host-shutdown recipe region found")
	}
	page := read(t, filepath.Join(docsRoot, "../core/hosting.md"))
	if !slices.Contains(goBlocks(page), shutdown) {
		t.Fatal("hosting must show the tested concurrent HTTP and Arc shutdown recipe")
	}
	if strings.Contains(page, "In your host, retain the same ownership order.") {
		t.Fatal("hosting must not recommend joining HTTP before canceling Arc observations")
	}
}

func TestEveryDocumentedGoBlockIsATestedRecipeRegion(t *testing.T) {
	sources := regions(t)
	if len(sources) == 0 {
		t.Fatal("no recipe regions found")
	}
	pages, err := filepath.Glob(filepath.Join(docsRoot, "*.md"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no recipe pages: %v", err)
	}
	used := map[string]bool{}
	for _, page := range pages {
		for i, block := range goBlocks(read(t, page)) {
			matched := false
			for name, source := range sources {
				if block == source {
					used[name], matched = true, true
				}
			}
			if !matched {
				t.Errorf("%s: Go block %d is not an exact recipe region:\n%s", filepath.Base(page), i+1, block)
			}
		}
	}
	var unused []string
	for name := range sources {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	slices.Sort(unused)
	if len(unused) > 0 {
		t.Errorf("recipe regions missing from the documentation: %q", unused)
	}
}
