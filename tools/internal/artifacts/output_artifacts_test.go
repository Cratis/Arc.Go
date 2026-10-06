// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const artifactProfile = `{"formatVersion":2,"name":"Tasks","openapi":{"title":"Tasks","version":"1"},"server":{"runtime":"arc-go"},"responseFields":{"Cratis.ValidationResult":{"state":{"absent":true}}}}`

const artifactCommands = `//arc:namespace Tasks
package commands
//arc:command
type Register struct { Name string; Count int32 }
func (Register) Handle() error { return nil }
`

const artifactQueries = `//arc:namespace Tasks
package listings
import "github.com/cratis/arc.go/queries"
//arc:readmodel
type Listing struct { Id int32; Label string }
type Filter struct { Prefix string }
//arc:query
func (Listing) All(Filter) (queries.Page[Listing], error) { return queries.Page[Listing]{}, nil }
`

func artifactConsumer(t *testing.T) string {
	t.Helper()
	dir := consumer(t)
	put(t, filepath.Join(dir, "commands", "model.go"), artifactCommands)
	put(t, filepath.Join(dir, "listings", "model.go"), artifactQueries)
	put(t, filepath.Join(dir, "profile.json"), artifactProfile)
	return dir
}

func artifactConfig(dir string, check bool, screenplay bool) Config {
	config := Config{Dir: dir, Patterns: []string{"./commands", "./listings"}, ConfigFile: filepath.Join(dir, "profile.json"), OpenAPIOut: "api/openapi.json", Check: check}
	if screenplay {
		config.ScreenplayOut = "docs/model.play"
	}
	return config
}

func TestArtifactsPublishCheckAndRemoveStaleWithoutTypeScript(t *testing.T) {
	dir := artifactConsumer(t)
	var report bytes.Buffer
	config := artifactConfig(dir, false, true)
	config.Report = &report
	generate(t, config)
	if !strings.Contains(report.String(), "openapi.json") || !strings.Contains(report.String(), "published") {
		t.Fatalf("publication not reported: %s", report.String())
	}
	openAPI := get(t, filepath.Join(dir, "api", "openapi.json"))
	var document map[string]any
	if err := json.Unmarshal(openAPI, &document); err != nil {
		t.Fatal(err)
	}
	if document["openapi"] != "3.1.1" || document[openAPIGeneratedMember] != openAPIGeneratedMarker || !ownedArtifact(openAPIRoot, openAPI) {
		t.Fatal("published OpenAPI document lacks its version or ownership marker")
	}
	paths := document["paths"].(map[string]any)
	if paths["/api/tasks/register"] == nil || paths["/api/tasks/all"] == nil {
		t.Fatal("published document lost application operations", paths)
	}
	play := get(t, filepath.Join(dir, "docs", "model.play"))
	if !bytes.HasPrefix(play, []byte(Header)) || !bytes.Contains(play, []byte("domain Tasks\n")) || !bytes.Contains(play, []byte("command Register")) {
		t.Fatalf("published Screenplay metadata is incomplete:\n%s", play)
	}
	manifest := get(t, filepath.Join(dir, manifestName))
	for _, entry := range []string{`"root": "openapi"`, `"path": "api/openapi.json"`, `"root": "screenplay"`, `"path": "docs/model.play"`} {
		if !bytes.Contains(manifest, []byte(entry)) {
			t.Fatalf("manifest lacks %s:\n%s", entry, manifest)
		}
	}
	if bytes.Contains(manifest, []byte(`"root": "go"`)) {
		t.Fatal("artifact-only manifest adopted marker-owned Go adapters")
	}
	generate(t, artifactConfig(dir, true, true))

	// A hand edit is detected by check mode and is never overwritten.
	edited := bytes.Replace(openAPI, []byte(`"Tasks"`), []byte(`"Edited"`), 1)
	put(t, filepath.Join(dir, "api", "openapi.json"), string(edited))
	for _, check := range []bool{true, false} {
		if err := Generate(t.Context(), artifactConfig(dir, check, true)); err == nil || !strings.Contains(err.Error(), "modified generated output") {
			t.Fatalf("check=%t accepted an edited document: %v", check, err)
		}
	}
	if !bytes.Equal(edited, get(t, filepath.Join(dir, "api", "openapi.json"))) {
		t.Fatal("edited document was overwritten")
	}
	put(t, filepath.Join(dir, "api", "openapi.json"), string(openAPI))

	// Dropping the Screenplay request is reported as stale by check mode and
	// removes only its owned file. The Go adapters are unaffected here because
	// the OpenAPI section still keeps wire analysis and endpoint verification on.
	if err := Generate(t.Context(), artifactConfig(dir, true, false)); err == nil || !strings.Contains(err.Error(), "stale: ") {
		t.Fatalf("check mode did not report the stale Screenplay file: %v", err)
	}
	generate(t, artifactConfig(dir, false, false))
	if _, err := os.Stat(filepath.Join(dir, "docs", "model.play")); !os.IsNotExist(err) {
		t.Fatal("stale owned Screenplay file was retained", err)
	}
	if !bytes.Equal(openAPI, get(t, filepath.Join(dir, "api", "openapi.json"))) {
		t.Fatal("unchanged document was rewritten")
	}
	generate(t, artifactConfig(dir, true, false))
	if err := Generate(t.Context(), artifactConfig(dir, true, true)); err == nil || !strings.Contains(err.Error(), "missing: ") {
		t.Fatalf("check mode did not report the missing Screenplay file: %v", err)
	}

	// A configured profile without artifacts still reconciles its manifest.
	// Dropping the openapi section also drops the wire graph, so check mode
	// first reports the Go adapters' changed endpoint expectations.
	put(t, filepath.Join(dir, "profile.json"), `{"formatVersion":2,"name":"Tasks"}`)
	none := artifactConfig(dir, true, false)
	none.OpenAPIOut = ""
	if err := Generate(t.Context(), none); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("check mode accepted the changed profile: %v", err)
	}
	if !bytes.Equal(openAPI, get(t, filepath.Join(dir, "api", "openapi.json"))) {
		t.Fatal("check mode changed the published document")
	}
	none.Check = false
	generate(t, none)
	if _, err := os.Stat(filepath.Join(dir, "api", "openapi.json")); !os.IsNotExist(err) {
		t.Fatal("stale owned OpenAPI document was retained", err)
	}
	none.Check = true
	generate(t, none)
}

func TestArtifactManifestDoesNotBlockUnrelatedAdapterInvocations(t *testing.T) {
	dir := artifactConsumer(t)
	generate(t, artifactConfig(dir, false, true))
	paths := []string{manifestName, "api/openapi.json", "docs/model.play"}
	before := map[string][]byte{}
	for _, path := range paths {
		before[path] = get(t, filepath.Join(dir, path))
	}
	for _, tc := range []struct {
		name     string
		profile  ApplicationProfile
		patterns []string
		tags     string
	}{
		{"different packages", ApplicationProfile{FormatVersion: 2, Name: "Tasks"}, []string{"./commands"}, ""},
		{"different profile", ApplicationProfile{FormatVersion: 2, Name: "Other"}, []string{"./commands", "./listings"}, ""},
		{"different tags", ApplicationProfile{FormatVersion: 2, Name: "Tasks"}, []string{"./commands", "./listings"}, "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := Config{Dir: dir, Profile: &tc.profile, Patterns: tc.patterns, Tags: tc.tags}
			generate(t, config)
			config.Check = true
			generate(t, config)
			for _, path := range paths {
				if !bytes.Equal(before[path], get(t, filepath.Join(dir, path))) {
					t.Fatalf("unrelated invocation changed %s", path)
				}
			}
		})
	}
}

func TestArtifactScreenplayOnlyChangesEndpointVerification(t *testing.T) {
	dir := artifactConsumer(t)
	put(t, filepath.Join(dir, "profile.json"), `{"formatVersion":2,"name":"Tasks"}`)
	config := artifactConfig(dir, false, false)
	config.OpenAPIOut = ""
	generate(t, config)
	adapter := filepath.Join(dir, "commands", Filename)
	plain := get(t, adapter)
	if bytes.Contains(plain, []byte("ExpectGeneratedEndpoints")) {
		t.Fatal("adapter-only invocation enabled endpoint verification")
	}
	config.ScreenplayOut = "docs/model.play"
	generate(t, config)
	if !bytes.Contains(get(t, adapter), []byte("ExpectGeneratedEndpoints")) {
		t.Fatal("Screenplay-only invocation omitted endpoint verification")
	}
	config.Check = true
	generate(t, config)
	config.ScreenplayOut = ""
	if err := Generate(t.Context(), config); err == nil || !strings.Contains(err.Error(), "generated adapters are stale") {
		t.Fatal("check without the Screenplay flag accepted changed adapters", err)
	}
	config.Check = false
	generate(t, config)
	if !bytes.Equal(plain, get(t, adapter)) {
		t.Fatal("dropping the sole wire consumer did not restore adapter-only verification")
	}
}

func TestArtifactsAreDeterministicAcrossPackageOrder(t *testing.T) {
	dir := artifactConsumer(t)
	generate(t, artifactConfig(dir, false, true))
	reordered := artifactConfig(dir, true, true)
	reordered.Patterns = []string{"./listings", "./commands"}
	generate(t, reordered)
}

func TestArtifactsRefuseUnsupportedShapesWithoutWriting(t *testing.T) {
	for _, tc := range []struct{ name, source, message string }{
		{"openapi float argument", "//arc:namespace Tasks\npackage listings\n//arc:readmodel\ntype Listing struct { Id int32 }\ntype Filter struct { Ratio float64 }\n//arc:query\nfunc (Listing) All(Filter) ([]Listing, error) { return nil, nil }\n", "special-value codec"},
		{"screenplay map field", "//arc:namespace Tasks\npackage listings\n//arc:readmodel\ntype Listing struct { Id int32; Tags map[string]string }\n//arc:query\nfunc (Listing) All() ([]Listing, error) { return nil, nil }\n", "screenplay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := artifactConsumer(t)
			put(t, filepath.Join(dir, "listings", "model.go"), tc.source)
			before := outputInventory(t, dir)
			config := artifactConfig(dir, false, true)
			if strings.HasPrefix(tc.name, "screenplay") {
				config.OpenAPIOut = ""
				put(t, filepath.Join(dir, "profile.json"), `{"formatVersion":2,"name":"Tasks"}`)
				before = outputInventory(t, dir)
			}
			err := Generate(t.Context(), config)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v; want %q", err, tc.message)
			}
			assertOutputInventory(t, dir, before)
		})
	}
}

func TestArtifactConfigurationRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, profile, message string
		change                 func(*Config)
	}{
		{"openapi flag without section", `{"formatVersion":2,"name":"Tasks"}`, "requires an openapi profile section", nil},
		{"openapi section without output", artifactProfile, "requires an output file", func(c *Config) { c.OpenAPIOut = "" }},
		{"wrong suffix", artifactProfile, "safe module-relative .json", func(c *Config) { c.OpenAPIOut = "api/openapi.yaml" }},
		{"escaping path", artifactProfile, "safe module-relative .json", func(c *Config) { c.OpenAPIOut = "../openapi.json" }},
		{"absolute outside module", artifactProfile, "inside the module", func(c *Config) { c.OpenAPIOut = filepath.Join(os.TempDir(), "elsewhere", "openapi.json") }},
		{"manifest name", artifactProfile, "safe module-relative .json", func(c *Config) { c.OpenAPIOut = manifestName }},
		{"screenplay with v1 profile", `{"formatVersion":1,"name":"Tasks"}`, "formatVersion 2", func(c *Config) { c.OpenAPIOut = ""; c.ScreenplayOut = "model.play" }},
		{"screenplay suffix", `{"formatVersion":2,"name":"Tasks"}`, "safe module-relative .play", func(c *Config) { c.OpenAPIOut = ""; c.ScreenplayOut = "model.txt" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := artifactConsumer(t)
			put(t, filepath.Join(dir, "profile.json"), tc.profile)
			before := outputInventory(t, dir)
			config := artifactConfig(dir, false, false)
			if tc.change != nil {
				tc.change(&config)
			}
			err := Generate(t.Context(), config)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v; want %q", err, tc.message)
			}
			assertOutputInventory(t, dir, before)
		})
	}
}

func TestArtifactsRefuseUnmanifestedExistingFile(t *testing.T) {
	dir := artifactConsumer(t)
	put(t, filepath.Join(dir, "api", "openapi.json"), "{\"openapi\":\"3.1.1\"}\n")
	before := outputInventory(t, dir)
	if err := Generate(t.Context(), artifactConfig(dir, false, false)); err == nil || !strings.Contains(err.Error(), "no manifest ownership") {
		t.Fatalf("handwritten document was adopted: %v", err)
	}
	assertOutputInventory(t, dir, before)
}

func TestArtifactsJoinTheTypeScriptManifest(t *testing.T) {
	dir := artifactConsumer(t)
	config := artifactConfig(dir, false, true)
	config.TypeScriptOut = "web"
	generate(t, config)
	manifest := get(t, filepath.Join(dir, "web", manifestName))
	for _, entry := range []string{`"root": "openapi"`, `"root": "screenplay"`, `"root": "ts"`, `"root": "go"`} {
		if !bytes.Contains(manifest, []byte(entry)) {
			t.Fatalf("TypeScript manifest lacks %s:\n%s", entry, manifest)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, manifestName)); !os.IsNotExist(err) {
		t.Fatal("a second module-root manifest was written", err)
	}
	config.Check = true
	generate(t, config)
}

func TestArcGenCLIPublishesArtifactsAndChecks(t *testing.T) {
	dir := artifactConsumer(t)
	binary := buildArcGenCLI(t)
	run := func(extra ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		args := append([]string{"-dir", dir, "-config", filepath.Join(dir, "profile.json"), "-openapi-out", "api/openapi.json", "-screenplay-out", filepath.Join(dir, "docs", "model.play")}, extra...)
		command := exec.CommandContext(ctx, binary, append(args, "./commands", "./listings")...)
		command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
		return command.CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("arc-gen artifact publication failed: %v\n%s", err, output)
	}
	tidyConsumer(t, dir)
	if output, err := run("-check"); err != nil || !bytes.Contains(output, []byte("verified")) {
		t.Fatalf("arc-gen artifact check failed: %v\n%s", err, output)
	}
	if !ownedArtifact(screenplayRoot, get(t, filepath.Join(dir, "docs", "model.play"))) {
		t.Fatal("absolute -screenplay-out did not publish the module-relative file")
	}
}
