// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzedQueryRejectsPinnedRouteHelperMetacharacters(t *testing.T) {
	for _, name := range []string{"a[", "a.b", "a$", "a\\\\b"} {
		t.Run(name, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "query.go"), "//arc:namespace Shop\npackage consumer\n//arc:readmodel\ntype Listing struct { Name string }\ntype Args struct { Value string `json:\""+name+"\"` }\nfunc (Listing) Find(Args) (Listing,error) { panic(\"must not execute\") }\n")
			graph, err := buildGraph(graphPackages(t, dir, "."), ApplicationProfile{FormatVersion: GraphVersion, Name: "parameters"}, true)
			if err != nil {
				t.Fatal(err)
			}
			output, err := renderTypeScriptQueries(graph)
			if err == nil || !strings.Contains(err.Error(), "22.48.2 route parameter helper") || output != nil {
				t.Fatal("unsafe parameter accepted", err)
			}
		})
	}
}

func TestAnalyzedQueryDefaultsUseOriginalGoScalarGrammarAndRange(t *testing.T) {
	for _, test := range []struct {
		typ, value string
		valid      bool
	}{
		{"bool", "1", false}, {"bool", "TRUE", true}, {"int", "1.5", false}, {"uint8", "256", false}, {"uint8", "255", true}, {"int8", "-129", false}, {"int8", "-128", true}, {"uint", "-1", false}, {"float32", "3.5e38", false}, {"float64", "1.5", true},
	} {
		t.Run(test.typ+"="+test.value, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "query.go"), "//arc:namespace Shop\npackage consumer\n//arc:readmodel\ntype Listing struct { Name string }\ntype Args struct { Value "+test.typ+" `query:\"default="+test.value+"\"` }\nfunc (Listing) Find(Args) (Listing,error) { panic(\"must not execute\") }\n")
			graph, err := buildGraph(graphPackages(t, dir, "."), ApplicationProfile{FormatVersion: GraphVersion, Name: "defaults"}, true)
			if !test.valid {
				if err == nil || !strings.Contains(err.Error(), "unsupported server default") {
					t.Fatal("invalid Go default accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := renderTypeScriptQueries(graph); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnalyzedSnapshotRejectsNullableCollectionElements(t *testing.T) {
	for _, shape := range []string{"[]*Listing", "[2]*Listing", "queries.Page[*Listing]"} {
		t.Run(shape, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "query.go"), "//arc:namespace Shop\npackage consumer\nimport \"github.com/cratis/arc.go/queries\"\nvar _ queries.NoArguments\n//arc:readmodel\ntype Listing struct { Name string }\nfunc (Listing) All() ("+shape+",error) { panic(\"must not execute\") }\n")
			graph, err := buildGraph(graphPackages(t, dir, "."), ApplicationProfile{FormatVersion: GraphVersion, Name: "nullable"}, true)
			if err != nil {
				t.Fatal(err)
			}
			if graph.Queries[0].Result.Element == nil || !graph.Queries[0].Result.Element.Nullable {
				t.Fatal("actual nullable element contract erased")
			}
			output, err := renderTypeScriptQueries(graph)
			if err == nil || !strings.Contains(err.Error(), "unsupported snapshot collection") || output != nil {
				t.Fatal("nullable element proxy accepted", err)
			}
		})
	}
}
