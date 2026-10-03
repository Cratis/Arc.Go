// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cratis/arc.go/internal/modelshape"
	"golang.org/x/tools/go/packages"
)

func graphPackages(t *testing.T, dir string, patterns ...string) []*analysis {
	t.Helper()
	loaded, err := packages.Load(&packages.Config{Context: t.Context(), Dir: dir, Mode: packages.NeedName | packages.NeedFiles | packages.NeedModule | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedTypesSizes}, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	var result []*analysis
	for _, pkg := range loaded {
		if len(pkg.Errors) != 0 {
			t.Fatal(pkg.Errors)
		}
		analysis, err := analyze(pkg)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, analysis)
	}
	return result
}

func TestWireGraphClosesOnlyClientContracts(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "model.go"), `//arc:namespace Shop.Tasks
package consumer
import (
 "time"
 "github.com/cratis/arc.go/concepts"
 "github.com/cratis/arc.go/commands"
 "github.com/cratis/arc.go/queries"
)
//arc:enum flags=true members=StatusNone:None,StatusDraft:Draft,StatusPublished:Published
type Status int
const (StatusNone Status = 0; StatusDraft Status = 1; StatusPublished Status = 2)
type Detail struct {
 ID concepts.UUID `+"`json:\"id\"`"+`
 Created time.Time
 Status Status
 Values map[string]int
 Tags []string
 Description *string
}
type Service func() chan int
//arc:command
type Register struct { Name string; Enabled bool }
func (Register) Handle(Service) (commands.Outcome[concepts.UUID], error) { return commands.Respond(concepts.UUID{}), nil }
//arc:readmodel
//arc:authorize roles=Reader,Editor
type Listing struct { Name string `+"`json:\"name\" sortable:\"true\"`"+`; Detail Detail }
type FindArguments struct { ID concepts.UUID `+"`json:\"id\" query:\"required\"`"+`; Enabled bool `+"`query:\"default=false\"`"+` }
//arc:query
//arc:authorize roles=Editor,Admin
func (Listing) Find(args FindArguments) (*Listing, error) { return nil, nil }
func (Listing) All() (queries.Page[Listing], error) { return queries.Page[Listing]{}, nil }
`)
	analyses := graphPackages(t, dir, ".")
	graph, err := buildGraph(analyses, ApplicationProfile{FormatVersion: GraphVersion, Name: "fixture"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if graph.FormatVersion != 1 || len(graph.Fingerprint) != 64 || len(graph.Commands) != 1 || len(graph.Queries) != 2 {
		t.Fatal(graph)
	}
	if response := graph.Commands[0].Response; response == nil || response.Kind != "Guid" {
		t.Fatal(response)
	}
	var detail, status *TypeDescriptor
	for i := range graph.Types {
		node := &graph.Types[i]
		if strings.HasSuffix(node.Key, ".Service") || strings.HasSuffix(node.Key, ".FindArguments") {
			t.Fatal("service or argument container entered model graph", node)
		}
		if node.Name.Name == "Detail" {
			detail = node
		}
		if node.Name.Name == "Status" {
			status = node
		}
	}
	if detail == nil || status == nil || !status.Flags || status.Members[1].Name != "draft" {
		t.Fatal(detail, status)
	}
	if detail.Fields[0].Type.Kind != "Guid" || detail.Fields[1].Type.Kind != "Date" || !detail.Fields[5].Optional {
		t.Fatal(detail.Fields)
	}
	var find, all *QueryDescriptor
	for i := range graph.Queries {
		if graph.Queries[i].Declaration.Name == "Find" {
			find = &graph.Queries[i]
		} else {
			all = &graph.Queries[i]
		}
	}
	if find == nil || all == nil || !all.Paged || all.Result.Kind != "array" || !reflect.DeepEqual(all.SortFields, []string{"name"}) {
		t.Fatal(find, all)
	}
	if !find.Parameters[0].Required || !find.Parameters[1].HasDefault || !reflect.DeepEqual(find.Roles, []string{"Editor", "Admin", "Reader"}) {
		t.Fatal(find)
	}
	if _, err := json.Marshal(graph); err != nil {
		t.Fatal("projection is not serializable", err)
	}
}

type fieldEmbedded struct {
	URL  string
	Name string
	Hide string `json:"-"`
}
type fieldHidden struct{ Name string }
type fieldCorpus struct {
	fieldEmbedded
	fieldHidden
	ID       string `json:"id"`
	Optional *int   `json:",omitempty"`
	Zero     bool   `json:",omitzero"`
}

func TestCompilerAndReflectAdaptersUseTheSameFieldCorpus(t *testing.T) {
	source := "package corpus\n" + `type fieldEmbedded struct { URL string; Name string; Hide string ` + "`json:\"-\"`" + ` }
type fieldHidden struct { Name string }
type fieldCorpus struct {
 fieldEmbedded
 fieldHidden
 ID string ` + "`json:\"id\"`" + `
 Optional *int ` + "`json:\",omitempty\"`" + `
 Zero bool ` + "`json:\",omitzero\"`" + `
}`
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "corpus.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := new(types.Config).Check("corpus", set, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	static, err := compilerFields(pkg.Scope().Lookup("fieldCorpus").Type())
	if err != nil {
		t.Fatal(err)
	}
	dynamic, err := modelshape.Fields(reflect.TypeFor[fieldCorpus]())
	if err != nil {
		t.Fatal(err)
	}
	if len(static) != len(dynamic) || len(static) != 4 {
		t.Fatal(static, dynamic)
	}
	for i := range static {
		left, right := static[i], dynamic[i]
		if left.Name != right.Name || !reflect.DeepEqual(left.Index, right.Index) || left.Tag != right.Tag || left.Tagged != right.Tagged || left.OmitEmpty != right.OmitEmpty || left.OmitZero != right.OmitZero {
			t.Fatal(left, right)
		}
	}
}

func TestWireGraphDiagnosesUnprovedContracts(t *testing.T) {
	for _, test := range []struct{ name, declaration, field, message string }{
		{"concept", "type ID string; func (ID) ConceptValue() string { return \"\" }", "ID", "missing-codec"},
		{"pointer_concept", "type ID string; func (*ID) ConceptValue() string { return \"\" }", "ID", "pointer-method"},
		{"opaque", "type ID string; func (ID) MarshalJSON() ([]byte,error) { return nil,nil }", "ID", "opaque custom codec"},
		{"arbitrary_uuid", "", "[16]byte", "not inferred UUID"},
		{"interface", "", "any", "no implicit any"},
		{"rich_dictionary", "type Detail struct { ID string }", "map[string]Detail", "rich dictionary"},
		{"cycle", "type Detail struct { Next *Detail }", "Detail", "constructor reference cycle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "input.go"), "//arc:namespace Shop\npackage consumer\n"+test.declaration+"\n//arc:command\ntype Register struct { Value "+test.field+" }\nfunc(Register)Handle()error{return nil}\n")
			_, err := buildGraph(graphPackages(t, dir, "."), ApplicationProfile{FormatVersion: GraphVersion, Name: "fixture"}, true)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v; expected %q", err, test.message)
			}
		})
	}
}

func TestGeneratorProfilePreservesOmittedBooleanDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arc-gen.json")
	put(t, path, `{"formatVersion":1,"name":"fixture","defaultNamespace":"","routes":{"routePrefix":"v1"},"typescript":{"namespaceRoots":[{"namespace":"Shop","folder":""}]}}`)
	profile, err := readProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if options := profile.routeOptions(); options.RoutePrefix != "v1" || !options.IncludeCommandName || !options.IncludeQueryName || !options.EnableQueryHTTPMethod {
		t.Fatal(options)
	}
	for _, input := range []string{
		`{"formatVersion":1,"name":"fixture","unknown":true}`,
		`{"formatVersion":1,"name":"fixture","typescript":{"namespaceRoots":[{"namespace":"Shop","folder":"../escape"}]}}`,
		`{"formatVersion":1,"name":"fixture","typescript":{"namespaceRoots":[{"namespace":"Shop","folder":"one"},{"namespace":"Shop","folder":"two"}]}}`,
	} {
		put(t, path, input)
		if _, err := readProfile(path); err == nil {
			t.Fatal("accepted", input)
		}
	}
}
