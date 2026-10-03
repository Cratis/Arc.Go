// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
)

func TestContractGraphRetainsDerivedDefaultAndDiscriminatorShape(t *testing.T) {
	graph, err := contractGraph(t, `type Notice interface { notice() }
//arc:model
//arc:derived interface=Notice
type Ordinary struct { Title string }; func (Ordinary) notice() {}
//arc:model
//arc:derived id=1578f20a-cd63-456f-98aa-c97daf05d0fa base=Ordinary interface=Notice
type Urgent struct { Ordinary; Priority int }
//arc:command
type Save struct { Notice Notice }; func (Save) Handle() error { return nil }
`, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, node := range graph.Types {
		if node.Discriminator == nil {
			continue
		}
		count++
		if node.Discriminator.Property != "_derivedTypeId" || node.Discriminator.InputRequired || node.Discriminator.OutputRequired == node.Discriminator.Default {
			t.Fatal(node)
		}
		if node.Name.Name == "Ordinary" && (!node.Discriminator.Default || node.DerivedID != "") {
			t.Fatal(node)
		}
		if node.Name.Name == "Urgent" && (node.Discriminator.Default || node.Base != "example.test/consumer.Ordinary") {
			t.Fatal(node)
		}
	}
	if count != 2 {
		t.Fatal("missing concrete derivative contracts", graph.Types)
	}
	if !graph.Commands[0].Fields[0].Presence.InputNull || !graph.Commands[0].Fields[0].Presence.OmitNil {
		t.Fatal(graph.Commands[0].Fields)
	}
}

func TestContractGraphRejectsUnregisterableDerivedDiscriminators(t *testing.T) {
	_, err := contractGraph(t, `type Notice interface { notice() }
//arc:model
//arc:derived interface=Notice
type Ordinary struct { Title string }; func (Ordinary) notice() {}
//arc:model
//arc:derived id=invalid base=Ordinary interface=Notice
type Urgent struct { Ordinary; Priority int }
`, contractProfile(), false)
	if err == nil || !strings.Contains(err.Error(), "canonical UUID") {
		t.Fatal(err)
	}
}

func TestContractGraphOmissionUsesRuntimeShapeNotJustTags(t *testing.T) {
	source := `import (
 "time"
 "github.com/cratis/arc.go/serialization"
)
//arc:command
type Save struct {
 Fixed [2]int ` + "`json:\"fixed,omitempty\"`" + `
 Empty [0]int ` + "`json:\"empty,omitempty\"`" + `
 Clock time.Time ` + "`json:\"clock,omitempty\"`" + `
 Pointer *serialization.Optional[int]
}
func (Save) Handle() error { return nil }
`
	graph, err := contractGraph(t, source, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	fields := fieldsByName(graph.Commands[0].Fields)
	if !fields["fixed"].OmitEmpty || fields["fixed"].Presence.OmitEmpty || !fields["fixed"].Presence.OutputRequired {
		t.Fatal(fields["fixed"])
	}
	if !fields["empty"].Presence.OmitEmpty || fields["empty"].Presence.OutputRequired {
		t.Fatal(fields["empty"])
	}
	if fields["clock"].Presence.OmitEmpty || !fields["clock"].Presence.InputNull || !fields["clock"].Presence.OutputRequired {
		t.Fatal(fields["clock"])
	}
	if !fields["pointer"].Presence.OmitNil || !fields["pointer"].Presence.OmitMissing || fields["pointer"].Presence.InputMissing != "zero" {
		t.Fatal(fields["pointer"])
	}
	dynamic := "type ID int; func (ID) IsZero() bool { panic(\"analysis executed predicate\") }\n//arc:command\ntype Save struct { ID ID `json:\"id,omitzero\"` }; func (Save) Handle() error { return nil }"
	if _, err := contractGraph(t, dynamic, contractProfile(), false); err == nil || !strings.Contains(err.Error(), "dynamic omitzero") {
		t.Fatal(err)
	}
}

func TestContractProfileCompatibilityAndNormalizationAreExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	put(t, path, `{"formatVersion":1,"name":"legacy","server":{"runtime":"arc-go"}}`)
	if _, err := readProfile(path); err == nil || !strings.Contains(err.Error(), "formatVersion 2") {
		t.Fatal(err)
	}
	put(t, path, `{"formatVersion":2,"name":"explicit","openapi":{"title":"API","version":"1"},"server":{"runtime":"arc-go","http":{"unknownReader":true}}}`)
	if _, err := readProfile(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatal(err)
	}
	source := "//arc:command\ntype Save struct{}; func (Save) Handle() error { return nil }"
	profile := contractProfile()
	graph, err := contractGraph(t, source, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if graph.FormatVersion != 2 || profile.OpenAPI.Streaming != "" || graph.Profile.OpenAPI.Streaming != "error" || !reflect.DeepEqual(graph.Profile.OpenAPI.Servers, []string{"/"}) {
		t.Fatal(graph.Profile)
	}
	if graph.Assertions.Server.Environment != "Production" || graph.Assertions.DiscoveryExposure != "unmapped" || graph.Assertions.Server.HTTP.MaxQueryBytes != 8192 {
		t.Fatal(graph.Assertions)
	}
	profile.Server.Environment = "Development"
	profile.OpenAPI.Out = "ignored-path"
	dev, err := contractGraph(t, source, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Assertions.DiscoveryExposure != "anonymous" {
		t.Fatal(dev.Assertions)
	}
	profile.OpenAPI.Out = "another-path"
	dev2, err := contractGraph(t, source, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Fingerprint != dev2.Fingerprint || !reflect.DeepEqual(graph.Endpoints, dev.Endpoints) {
		t.Fatal("output roots or environment altered routes/identity")
	}
	if err := Generate(t.Context(), Config{Profile: &profile}); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatal("OpenAPI CLI publication became enabled", err)
	}
}

func TestContractGraphSecurityAssertionsDoNotComeFromRoutes(t *testing.T) {
	source := `//arc:command
//arc:authorize roles=Admin
type Save struct{}; func (Save) Handle() error { return nil }
`
	profile := contractProfile()
	profile.Server.Authentication = AuthenticationProfile{Schemes: map[string]SecurityScheme{"Token": {Type: "http", Scheme: "bearer"}}, Handlers: []string{"Token"}}
	graph, err := contractGraph(t, source, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Assertions.DiscoveryExposure != "authenticated" || graph.Assertions.SecurityVerification != "not-verified-by-route-expectations" {
		t.Fatal(graph.Assertions)
	}
	value := true
	profile.Server.Authorization.Policies = map[string]PolicyProfile{"Guest": {EvaluatesAnonymous: &value}}
	profile.Server.Authorization.Fallback = &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "Guest"}}}
	if _, err := contractGraph(t, source, profile, false); err != nil {
		t.Fatal(err)
	}
	profile.ResponseFields["Shop.Save"] = map[string]ResponseField{"response": {Schema: json.RawMessage(`{"type":"integer"}`)}}
	if _, err := contractGraph(t, source, profile, false); err == nil || !strings.Contains(err.Error(), "contradicts known response") {
		t.Fatal(err)
	}
}

func TestContractGraphCombinedFamiliesAdmitOnlyTheSelectedTypeScriptClosure(t *testing.T) {
	profile := contractProfile()
	profile.TypeScript.ExcludeTypes = []string{"Shop.Hidden"}
	graph, err := contractGraph(t, `import "github.com/cratis/arc.go/serialization"
//arc:command
type Hidden struct { Value serialization.Optional[int] }; func (Hidden) Handle() error { return nil }
//arc:command
type Visible struct { Name string }; func (Visible) Handle() error { return nil }
`, profile, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Commands) != 2 || len(graph.Endpoints) != 4 || len(graph.Commands[0].Fields) != 1 {
		t.Fatal(graph)
	}
	outputs, err := renderTypeScriptQueries(graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range outputs {
		if strings.Contains(output.path, "Hidden") {
			t.Fatal(output.path)
		}
	}
	if len(outputs) == 0 {
		t.Fatal("selected TS family disappeared")
	}
}

func TestContractGraphDoesNotUseTypeScriptExclusionToEraseAPIOperations(t *testing.T) {
	profile := contractProfile()
	profile.TypeScript.ExcludeTypes = []string{"Shop.Save"}
	graph, err := contractGraph(t, "//arc:command\ntype Save struct { Name string }; func (Save) Handle() error { return nil }", profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Commands[0].Excluded || graph.Commands[0].Input == nil || len(graph.Commands[0].Fields) != 1 || len(graph.Endpoints) != 2 {
		t.Fatal(graph.Commands, graph.Endpoints)
	}
}
