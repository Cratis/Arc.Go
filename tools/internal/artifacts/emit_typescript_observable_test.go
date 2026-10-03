// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cratis/arc.go/metadata"
)

func observableGraph(t *testing.T) *Graph {
	t.Helper()
	graph := queryGraph(t)
	graph.Types[0].Fields = append(graph.Types[0].Fields, scalarField("id", "string"))
	graph.Catalog.Queries[0].Observable = true
	graph.Queries[0].Declaration = graph.Catalog.Queries[0]
	graph.Queries[0].Delivery = "observable"
	var err error
	graph.Endpoints, err = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestObservableQueryPlannerAliasesItemTypesAndPreservesMetadata(t *testing.T) {
	graph := observableGraph(t)
	graph.Types[0].Name.Name = "ObservableQueryFor"
	graph.Catalog.Queries[0].ReadModel = graph.Types[0].Name
	graph.Queries[0].Declaration = graph.Catalog.Queries[0]
	graph.Queries[0].Parameters = []FieldDescriptor{{Name: "count", Type: WireType{Kind: "number"}, HasDefault: true, Default: "5"}}
	graph.Endpoints, _ = metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	outputs, err := renderTypeScriptQueries(graph)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, output := range outputs {
		if strings.HasSuffix(output.path, "All.ts") {
			text = string(output.content)
		}
	}
	for _, want := range []string{"ObservableQueryFor as ObservableQueryFor_2", "extends ObservableQueryFor_2<ObservableQueryFor[]", "getKey?: (item: ObservableQueryFor) => unknown", "super(ObservableQueryFor, true)", "count?: number", "count!: number"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "count: number =") {
		t.Fatal("server default manufactured client value")
	}
}

func TestObservableSpecificMembersAndPointerElementsPreventAllOutput(t *testing.T) {
	for _, member := range []string{"subscribe", "dispose", "validateArguments", "buildQueryArguments", "deserializeResult"} {
		graph := observableGraph(t)
		graph.Queries[0].Parameters[0].Name = member
		output, err := renderTypeScriptQueries(graph)
		if output != nil || err == nil || !strings.Contains(err.Error(), "query runtime") {
			t.Fatalf("reserved observable member %s: %v", member, err)
		}
	}
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), `//arc:namespace Shop
package consumer
import "github.com/cratis/arc.go/observable"
//arc:readmodel
type Task struct { ID string `+"`json:\"id\"`"+` }
func (Task) Watch() (observable.Source[[]*Task],error) { return nil,nil }
func (Task) All() ([]Task,error) { return nil,nil }
`)
	if err := Generate(t.Context(), Config{Dir: dir, TypeScriptOut: "web"}); err == nil || !strings.Contains(err.Error(), "collection") {
		t.Fatalf("unsupported nullable elements: %v", err)
	}
}
