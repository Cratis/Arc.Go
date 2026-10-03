// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func contractProfile() ApplicationProfile {
	return ApplicationProfile{FormatVersion: ContractGraphVersion, Name: "contract", OpenAPI: &OpenAPIProfile{Title: "Fixture", Version: "1"}, Server: &ServerProfile{Runtime: "arc-go"}, ResponseFields: map[string]map[string]ResponseField{"Cratis.ValidationResult": {"state": {Absent: true}}}}
}

func contractGraph(t *testing.T, source string, profile ApplicationProfile, ts bool) (*Graph, error) {
	t.Helper()
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), "//arc:namespace Shop\npackage consumer\n"+source)
	return buildGraph(graphPackages(t, dir, "."), profile, ts)
}

func fieldsByName(fields []FieldDescriptor) map[string]FieldDescriptor {
	result := map[string]FieldDescriptor{}
	for _, field := range fields {
		result[field.Name] = field
	}
	return result
}

func TestContractGraphRetainsExactScalarsAndArrays(t *testing.T) {
	graph, err := contractGraph(t, `//arc:enum flags=true
type Wide uint64
const (Small Wide = 1; Largest Wide = 18446744073709551615)
//arc:command
type Save struct { Signed int64; Unsigned uint64; Narrow int8; Count uint; Float float32; Double float64; Fixed [3]int16; Empty [0]string; Choice Wide; Bytes [16]byte }
func (Save) Handle() error { return nil }
`, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	fields := fieldsByName(graph.Commands[0].Fields)
	for _, tc := range []struct {
		name, representation, minimum, maximum string
		bits                                   int
		unsigned                               bool
	}{
		{"signed", "integer", "-9223372036854775808", "9223372036854775807", 64, false},
		{"unsigned", "integer", "0", "18446744073709551615", 64, true},
		{"narrow", "integer", "-128", "127", 8, false},
		{"float", "float", "", "", 32, false},
		{"double", "float", "", "", 64, false},
	} {
		scalar := fields[tc.name].Type.Contract.Scalar
		if scalar.Representation != tc.representation || scalar.Minimum != tc.minimum || scalar.Maximum != tc.maximum || scalar.Bits != tc.bits || scalar.Unsigned != tc.unsigned {
			t.Fatalf("%s: %+v", tc.name, scalar)
		}
	}
	if got := fields["float"].Type.Contract.Scalar.SpecialValues; !reflect.DeepEqual(got, []string{"NaN", "Infinity", "-Infinity"}) {
		t.Fatal(got)
	}
	if got := fields["fixed"].Type.Contract.FixedLength; got == nil || *got != 3 {
		t.Fatal(got)
	}
	if got := fields["empty"].Type.Contract.FixedLength; got == nil || *got != 0 {
		t.Fatal(got)
	}
	if bytes := fields["bytes"].Type; bytes.Kind != "array" || bytes.Contract.FixedLength == nil || *bytes.Contract.FixedLength != 16 || bytes.Element.Contract.Scalar.Maximum != "255" || !bytes.Contract.ByteArray || bytes.Contract.ArrayInputLength != "zero-fill-or-truncate" {
		t.Fatal(bytes)
	}
	if !fields["fixed"].Presence.OutputRequired || fields["fixed"].Presence.InputRequired {
		t.Fatal(fields["fixed"])
	}
	var enum *TypeDescriptor
	for i := range graph.Types {
		if graph.Types[i].Kind == "enum" {
			enum = &graph.Types[i]
		}
	}
	if enum == nil || !enum.Flags || enum.Scalar.Maximum != "18446744073709551615" || enum.Members[1].Value != "18446744073709551615" {
		t.Fatal(enum)
	}
	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "18446744073709551615") || strings.Contains(string(data), "go/types") {
		t.Fatal(string(data))
	}
	if graph.Assertions.Provenance != "application-profile-assertion" || graph.Assertions.SecurityVerification != "not-verified-by-route-expectations" {
		t.Fatal(graph.Assertions)
	}
}

type contractEmbedded struct {
	Promoted int `json:"promoted"`
}
type contractInput struct {
	*contractEmbedded
	Number   int                         `json:"number"`
	Pointer  *int                        `json:"pointer"`
	Empty    int                         `json:"empty,omitempty"`
	Zero     int                         `json:"zero,omitzero"`
	List     []*int                      `json:"list"`
	Fixed    [2]int                      `json:"fixed"`
	Optional serialization.Optional[int] `json:"optional"`
}

func TestContractByteArrayBindingDiffersFromOtherFixedArrays(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  [2]byte
	}{
		{`[1]`, [2]byte{1, 0}}, {`[1,2,3]`, [2]byte{1, 2}},
	} {
		var value [2]byte
		if err := serialization.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Fatal(err)
		}
		if value != tc.want {
			t.Fatal(value, tc.want)
		}
		var ordinary [2]int
		if err := serialization.Unmarshal([]byte(tc.input), &ordinary); err == nil {
			t.Fatal("ordinary array accepted byte-array length semantics")
		}
	}
	var value [2]byte
	if err := serialization.Unmarshal([]byte(`null`), &value); err == nil {
		t.Fatal("nonnull byte array accepted null")
	}
}

func TestContractGraphPresenceMatchesIndependentWireExamples(t *testing.T) {
	source := "import \"github.com/cratis/arc.go/serialization\"\n" + `type Embedded struct { Promoted int ` + "`json:\"promoted\"`" + ` }
//arc:command
type Save struct {
 *Embedded
 Number int ` + "`json:\"number\"`" + `
 Pointer *int ` + "`json:\"pointer\"`" + `
 Empty int ` + "`json:\"empty,omitempty\"`" + `
 Zero int ` + "`json:\"zero,omitzero\"`" + `
 List []*int ` + "`json:\"list\"`" + `
 Fixed [2]int ` + "`json:\"fixed\"`" + `
 Optional serialization.Optional[int] ` + "`json:\"optional\"`" + `
}
func (Save) Handle() error { return nil }
`
	graph, err := contractGraph(t, source, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	fields := fieldsByName(graph.Commands[0].Fields)
	for _, tc := range []struct {
		name, missing           string
		null, required, outnull bool
	}{
		{"number", "zero", false, true, false}, {"pointer", "zero", true, false, false},
		{"empty", "zero", false, false, false}, {"zero", "zero", false, false, false},
		{"list", "zero", true, false, false}, {"fixed", "zero", false, true, false},
		{"optional", "missing", true, false, true}, {"promoted", "zero", false, false, false},
	} {
		presence := fields[tc.name].Presence
		if presence.InputMissing != tc.missing || presence.InputNull != tc.null || presence.OutputRequired != tc.required || presence.OutputNull != tc.outnull || presence.InputRequired {
			t.Fatalf("%s: %+v", tc.name, presence)
		}
	}
	if !fields["promoted"].Presence.EmbeddedParentNullable || !fields["list"].Type.Element.Contract.OutputNull || fields["optional"].Type.Contract.Presence != "missing-null-value" {
		t.Fatal(fields)
	}
	// These expected instances are source-derived, not generated from the graph.
	var cases []struct {
		Input  string `json:"input"`
		Output string `json:"output"`
		Valid  bool   `json:"valid"`
	}
	if err := json.Unmarshal(get(t, filepath.Join("testdata", "wire-contract", "presence.json")), &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 6 {
		t.Fatal("incomplete presence fixture", len(cases))
	}
	for _, example := range cases {
		tc := struct {
			input, output string
			valid         bool
		}{example.Input, example.Output, example.Valid}
		var value contractInput
		err := serialization.Unmarshal([]byte(tc.input), &value)
		if (err == nil) != tc.valid {
			t.Fatalf("bind %s: %v", tc.input, err)
		}
		if !tc.valid {
			continue
		}
		output, err := serialization.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(output) != tc.output {
			t.Fatalf("%s: got %s want %s", tc.input, output, tc.output)
		}
	}
}

func TestContractAnalysisDoesNotRelaxTypeScriptAdmission(t *testing.T) {
	for _, tc := range []struct{ name, source, message string }{
		{"optional", `import "github.com/cratis/arc.go/serialization"
//arc:command
type Save struct { Value serialization.Optional[int] }; func (Save) Handle() error { return nil }`, "serialization.Optional"},
		{"byte-array", `//arc:command
type Save struct { Value [16]byte }; func (Save) Handle() error { return nil }`, "not inferred UUID"},
		{"cycle", `type Link struct { Next *Link }
//arc:command
type Save struct { Value Link }; func (Save) Handle() error { return nil }`, "constructor reference cycle"},
		{"dictionary", `type Item struct { Name string }
//arc:command
type Save struct { Value map[string]Item }; func (Save) Handle() error { return nil }`, "rich dictionary"},
		{"symbol", "//arc:command\ntype Save struct { Value int `json:\"__proto__\"` }; func (Save) Handle() error { return nil }", "reserved frontend"},
		{"wideflags", `//arc:enum flags=true
type Wide uint64; const Large Wide = 18446744073709551615
//arc:command
type Save struct { Value Wide }; func (Save) Handle() error { return nil }`, "safe number/bitwise"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := contractGraph(t, tc.source, contractProfile(), false); err != nil {
				t.Fatal("OpenAPI analysis:", err)
			}
			if _, err := contractGraph(t, tc.source, contractProfile(), true); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("TS admission: %v, want %s", err, tc.message)
			}
		})
	}
}

func TestContractGraphQueryBindingDefaultsAndRequiredness(t *testing.T) {
	source := `import (
 "github.com/cratis/arc.go/serialization"
 "github.com/cratis/arc.go/concepts"
)
//arc:readmodel
type Listing struct { Name string }
type Args struct {
 ID *int ` + "`query:\"required\"`" + `
 Enabled bool ` + "`query:\"default=false\"`" + `
 Wide uint64 ` + "`query:\"default=18446744073709551615\"`" + `
 Value serialization.Optional[string] ` + "`query:\"preservePresence\"`" + `
 Tags []string
 OptionalTags serialization.Optional[[]int]
 Bytes [2]byte
 Key concepts.UUID
}
//arc:query http=GET
func (Listing) Find(Args) (Listing, error) { return Listing{}, nil }
`
	graph, err := contractGraph(t, source, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	params := fieldsByName(graph.Queries[0].Parameters)
	if !params["ID"].Required || params["ID"].Binding.Missing != "error" || params["ID"].Presence.InputRequired {
		t.Fatal(params)
	}
	if params["enabled"].Default != "false" || params["enabled"].Binding.Missing != "default" || params["wide"].Default != "18446744073709551615" {
		t.Fatal(params)
	}
	if params["value"].Binding.EmptyAsMissing || params["value"].Binding.NullAsMissing || !params["tags"].Binding.CaseInsensitive || params["tags"].Binding.Encoding != "csv-or-json-array" {
		t.Fatal(params)
	}
	if params["optionalTags"].Binding.Encoding != "csv-or-json-array" || params["bytes"].Binding.FixedLength == nil || *params["bytes"].Binding.FixedLength != 2 || params["key"].Binding.Encoding != "scalar-text" || params["key"].Binding.FixedLength != nil {
		t.Fatal(params)
	}
	if len(graph.Endpoints) != 1 || graph.Endpoints[0].Method != "GET" {
		t.Fatal(graph.Endpoints)
	}
}

func TestContractGraphExplicitCodecAndConceptBoundary(t *testing.T) {
	source := `type ID uint64
func (ID) ConceptValue() uint64 { panic("analysis executed marker") }
func (ID) MarshalJSON() ([]byte,error) { panic("analysis executed codec") }
func (*ID) UnmarshalJSON([]byte) error { panic("analysis executed codec") }
func (ID) MarshalText() ([]byte,error) { panic("analysis executed codec") }
func (*ID) UnmarshalText([]byte) error { panic("analysis executed codec") }
//arc:command
type Save struct { ID *ID }; func (Save) Handle() error { panic("analysis executed handler") }
`
	profile := contractProfile()
	if _, err := contractGraph(t, source, profile, false); err == nil || !strings.Contains(err.Error(), "concept codec requires") {
		t.Fatal(err)
	}
	profile.WireSchemas = map[string]WireSchemas{"example.test/consumer.ID": {Input: json.RawMessage(`{"type":"integer","maximum":18446744073709551615}`), Output: json.RawMessage(`{"type":"integer"}`)}}
	graph, err := contractGraph(t, source, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	wire := graph.Commands[0].Fields[0].Type.Contract
	if wire.Concept != "example.test/consumer.ID" || wire.Scalar.Maximum != "18446744073709551615" || wire.Schemas == nil || wire.PointerDepth != 1 {
		t.Fatal(wire)
	}
}

func TestContractGraphResponsesAndFrameworkRequiredArrays(t *testing.T) {
	graph, err := contractGraph(t, `import "github.com/cratis/arc.go/commands"
//arc:command
type None struct{}; func (None) Handle() (commands.NoResponse, error) { return commands.NoResponse{}, nil }
//arc:command
type Value struct{}; func (Value) Handle() (commands.Outcome[int], error) { return commands.Respond(0), nil }
`, contractProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Commands[0].ResponseKind != "none" || graph.Commands[1].ResponseKind != "value" || graph.Commands[1].Response.Contract.Scalar.Representation != "integer" {
		t.Fatal(graph.Commands)
	}
	for _, contract := range graph.Framework {
		for _, field := range contract.Fields {
			if field.Name == "members" || field.Name == "validationResults" || field.Name == "exceptionMessages" {
				if !field.Presence.OutputRequired || field.Presence.OutputNull || field.Optional || field.Type.Nullable {
					t.Fatal(contract.Key, field)
				}
			}
		}
	}
	source := "//arc:command\ntype Save struct{}; func (Save) Handle() (int, error) { return 0, nil }"
	if _, err := contractGraph(t, source, contractProfile(), false); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal(err)
	}
	profile := contractProfile()
	profile.ResponseFields["Shop.Save"] = map[string]ResponseField{"response": {Schema: json.RawMessage(`{"type":"integer"}`)}}
	if graph, err := contractGraph(t, source, profile, false); err != nil || graph.Commands[0].Response.Contract.Schemas == nil {
		t.Fatal(graph, err)
	}
}

func TestFrameworkArrayAndPayloadExamplesAreIndependentOfClientOptionality(t *testing.T) {
	for _, value := range []any{commands.Success([16]byte{}), queries.NotReady[int]([16]byte{}), validation.Result{}, identity.View[any]{}} {
		data, err := serialization.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"validationResults", "exceptionMessages", "members", "roles"} {
			if field, exists := object[name]; exists && string(field) != "[]" {
				t.Fatalf("%s: %s", name, data)
			}
		}
		if _, exists := object["data"]; exists {
			t.Fatal(string(data))
		}
		if _, exists := object["response"]; exists {
			t.Fatal(string(data))
		}
		if field, exists := object["details"]; exists && string(field) != "null" {
			t.Fatal(string(data))
		}
	}
}

func TestContractProfileAssertionsRejectUnknownAndContradictoryReferences(t *testing.T) {
	source := "//arc:command\ntype Save struct{}; func (Save) Handle() error { return nil }"
	for _, tc := range []struct {
		name, want string
		mutate     func(*ApplicationProfile)
	}{
		{"runtime", "server.runtime", func(p *ApplicationProfile) { p.Server.Runtime = "unknown" }},
		{"unknown-handler", "unknown server authentication scheme", func(p *ApplicationProfile) { p.Server.Authentication.Handlers = []string{"missing"} }},
		{"guest-policy", "requires explicit evaluatesAnonymous", func(p *ApplicationProfile) { p.Server.Authorization.Policies = map[string]PolicyProfile{"guest": {}} }},
		{"unknown-policy", "unknown", func(p *ApplicationProfile) {
			p.Server.Authorization.Fallback = &metadata.Authorization{Requirements: []metadata.AuthorizationRequirement{{Policy: "missing"}}}
		}},
		{"discovery-enforcement", "require authentication handlers", func(p *ApplicationProfile) { value := true; p.Server.Introspection.RequireAuthentication = &value }},
		{"contradiction", "contradict", func(p *ApplicationProfile) {
			value := false
			p.Server.Introspection.RequireAuthentication = &value
			p.Server.Introspection.Roles = []string{"admin"}
		}},
		{"unknown-artifact", "unknown responseFields artifact", func(p *ApplicationProfile) {
			p.ResponseFields["missing"] = map[string]ResponseField{"state": {Absent: true}}
		}},
		{"unknown-type", "unknown wire type reference", func(p *ApplicationProfile) {
			p.ResponseFields["Cratis.ValidationResult"]["state"] = ResponseField{Type: "missing.Type"}
		}},
		{"unknown-schema", "unknown or unreachable", func(p *ApplicationProfile) {
			p.WireSchemas = map[string]WireSchemas{"missing.Type": {Input: json.RawMessage(`{"type":"string"}`), Output: json.RawMessage(`{"type":"string"}`)}}
		}},
		{"implicit-any", "unconstrained", func(p *ApplicationProfile) {
			p.ResponseFields["Cratis.ValidationResult"]["state"] = ResponseField{Schema: json.RawMessage(`{}`)}
		}},
		{"unknown-ref", "references require", func(p *ApplicationProfile) {
			p.ResponseFields["Cratis.ValidationResult"]["state"] = ResponseField{Schema: json.RawMessage(`{"type":"object","properties":{"item":{"$ref":"#/components/schemas/Missing"}}}`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := contractProfile()
			tc.mutate(&profile)
			if _, err := contractGraph(t, source, profile, false); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want %s", err, tc.want)
			}
		})
	}
}
