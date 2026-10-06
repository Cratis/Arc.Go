// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"strings"
	"testing"
)

func TestModelRendererRejectsSymbolAndReferencedSymbol(t *testing.T) {
	for _, referenced := range []bool{false, true} {
		name := "export"
		nodes := []TypeDescriptor{modelNode("symbol", "Shop", "Symbol", scalarField("name", "string"))}
		if referenced {
			name = "reference"
			nodes = append(nodes, modelNode("container", "Shop", "Container", FieldDescriptor{Name: "value", Type: WireType{Kind: "model", Target: "symbol"}}))
		}
		t.Run(name, func(t *testing.T) {
			outputs, err := renderTypeScriptModels(modelGraph(nodes...))
			if outputs != nil || err == nil || !strings.Contains(err.Error(), `invalid TypeScript export name "Symbol"`) {
				t.Fatalf("outputs %v, error %v", outputs, err)
			}
		})
	}
}

func TestTypeScriptImportsReserveGlobalSymbol(t *testing.T) {
	imports := &tsImports{requests: map[tsImport]bool{}, aliases: map[tsImport]string{}, own: "Container"}
	key := imports.add("./Symbols", "Symbol", true)
	lines := imports.resolve()
	if imports.aliases[key] != "Symbol_2" || len(lines) != 1 || lines[0] != `import { Symbol as Symbol_2 } from "./Symbols";` {
		t.Fatal(lines, imports.aliases)
	}
}
