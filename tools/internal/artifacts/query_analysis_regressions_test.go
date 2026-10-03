// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"path/filepath"
	"strings"
	"testing"
)

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
