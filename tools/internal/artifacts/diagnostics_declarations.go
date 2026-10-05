// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	goanalysis "golang.org/x/tools/go/analysis"
)

// DeclarationAnalyzer checks Arc authoring conventions before generator admission.
// It uses the driver's type graph, ignores generated files and arc:ignore, and
// never loads packages or executes user code. Diagnostics are advisory; arc-gen
// and runtime registration remain the admission authorities.
var DeclarationAnalyzer = &goanalysis.Analyzer{
	Name:      "arcdeclarations",
	Doc:       "report ARC0001-0006, ARC0014 and ARC0019 on Arc declarations",
	Run:       runDeclarationDiagnostics,
	FactTypes: []goanalysis.Fact{new(declarationFact)},
}

// declarationFact carries only opted-in identities, never source names or paths.
// The analysis driver transports these across imports for the selected build.
type declarationFact struct{ Kind string }

func (*declarationFact) AFact() {}

type diagnosticDeclaration struct {
	kind    string
	ignored bool
	query   bool
	model   string
}

// diagnosticDirectives reads only the declaration markers needed by these rules.
// Deliberately do not call parseDirectives: contradictory authorization and
// generic query declarations must be reportable before arc-gen rejects them.
func diagnosticDirectives(group *ast.CommentGroup) diagnosticDeclaration {
	var d diagnosticDeclaration
	if group == nil {
		return d
	}
	for _, comment := range group.List {
		words := strings.Fields(strings.TrimSpace(strings.TrimPrefix(comment.Text, "//")))
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "arc:command", "arc:readmodel", "arc:validator":
			d.kind = strings.TrimPrefix(words[0], "arc:")
		case "arc:query":
			d.query = true
			for _, word := range words[1:] {
				if value, ok := strings.CutPrefix(word, "model="); ok {
					d.model = value
				}
			}
		case "arc:ignore":
			d.ignored = true
		}
	}
	return d
}

func runDeclarationDiagnostics(pass *goanalysis.Pass) (any, error) {
	declarations := map[types.Type]diagnosticDeclaration{}
	methods := map[types.Type]map[string]*ast.FuncDecl{}
	var functions []*ast.FuncDecl
	report := func(node ast.Node, code, message string) {
		pass.Report(goanalysis.Diagnostic{Pos: node.Pos(), End: node.End(), Category: code, Message: code + ": " + message})
	}
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		for _, declaration := range file.Decls {
			switch decl := declaration.(type) {
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					group := ts.Doc
					if group == nil && len(decl.Specs) == 1 {
						group = decl.Doc
					}
					d := diagnosticDirectives(group)
					// An alias is a reference, not a second model declaration.
					if ts.Assign.IsValid() {
						continue
					}
					t := pass.TypesInfo.Defs[ts.Name].Type()
					declarations[t] = d
					if !d.ignored && (d.kind == "command" || d.kind == "readmodel") {
						reportAuthorizationConflict(group, report)
					}
				}
			case *ast.FuncDecl:
				functions = append(functions, decl)
				if decl.Recv == nil {
					continue
				}
				sig := pass.TypesInfo.Defs[decl.Name].Type().(*types.Signature)
				t := diagnosticDeclarationType(sig.Recv().Type())
				if methods[t] == nil {
					methods[t] = map[string]*ast.FuncDecl{}
				}
				if !diagnosticDirectives(decl.Doc).ignored {
					methods[t][decl.Name.Name] = decl
				}
			}
		}
	}
	for typ, d := range declarations {
		if named, ok := typ.(*types.Named); ok && !d.ignored && (d.kind == "command" || d.kind == "readmodel") && pass.ExportObjectFact != nil {
			pass.ExportObjectFact(named.Obj(), &declarationFact{Kind: d.kind})
		}
	}
	kindOf := func(t types.Type) string {
		t = diagnosticDeclarationType(t)
		if d, exists := declarations[t]; exists {
			if d.ignored {
				return ""
			}
			return d.kind
		}
		if named, ok := t.(*types.Named); ok && pass.ImportObjectFact != nil {
			var fact declarationFact
			if pass.ImportObjectFact(named.Obj(), &fact) {
				return fact.Kind
			}
		}
		return ""
	}
	// Iterate source order, never map order, to keep output reproducible.
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Assign.IsValid() {
					continue
				}
				t := pass.TypesInfo.Defs[ts.Name].Type()
				d := declarations[t]
				if d.ignored {
					continue
				}
				handle := methods[t]["Handle"]
				if d.kind == "command" {
					if handle == nil {
						report(ts.Name, "ARC0004", "command requires a directly declared exported Handle method; promoted or ignored methods do not count")
					} else if provide := methods[t]["Provide"]; provide != nil {
						reportUnusedPreparation(pass, handle, provide, report)
					}
				} else if d.kind == "" && handle != nil && commandLikeType(t) {
					report(ts.Name, "ARC0002", "type has exported data and Handle but no arc:command; add the directive or use arc:ignore for a non-command")
				}
			}
		}
	}
	for _, decl := range functions {
		d := diagnosticDirectives(decl.Doc)
		if d.ignored {
			continue
		}
		sig := pass.TypesInfo.Defs[decl.Name].Type().(*types.Signature)
		var receiver types.Type
		if sig.Recv() != nil {
			receiver = diagnosticBaseType(sig.Recv().Type())
		}
		owner := declarations[diagnosticDeclarationType(receiver)]
		if owner.ignored {
			continue
		}
		if decl.Name.Name == "Handle" && receiver != nil && owner.kind != "command" {
			for i := range sig.Params().Len() {
				if kindOf(sig.Params().At(i).Type()) == "command" {
					report(decl.Name, "ARC0003", "external Handle receives an arc:command; move handling to that command's Handle method")
					break
				}
			}
		}
		if owner.kind == "command" && (decl.Name.Name == "Handle" || decl.Name.Name == "Provide") || d.kind == "validator" && receiver == nil {
			for i := range sig.Params().Len() {
				parameter := sig.Params().At(i)
				if _, pointer := types.Unalias(parameter.Type()).(*types.Pointer); !pointer && kindOf(parameter.Type()) == "readmodel" {
					// Pointers are not unwrapped: they preserve absence in a
					// resolver that explicitly supports optional read models.
					node := ast.Node(decl.Name)
					if parameter.Pos().IsValid() {
						node = &ast.Ident{NamePos: parameter.Pos(), Name: parameter.Name()}
					}
					report(node, "ARC0006", "read-model dependency is a value; use an absence-aware pointer/resolver or keep required-dependency failure deliberately")
				}
			}
		}
		modelType := receiver
		if receiver == nil && d.query && d.model != "" {
			if object := pass.Pkg.Scope().Lookup(d.model); object != nil {
				modelType = types.Unalias(object.Type())
			}
		}
		isQuery := declarations[diagnosticDeclarationType(modelType)].kind == "readmodel" && (d.query || diagnosticQueryConvention(decl))
		if !isQuery {
			continue
		}
		reportAuthorizationConflict(decl.Doc, report)
		output, validResults := diagnosticOutput(sig)
		if output == nil {
			if d.query {
				report(decl.Name, "ARC0001", "query must return (owning-model shape, error)")
			}
			continue
		}
		emission, source, sourceError := sourceEmission(output)
		if source {
			output = emission
		}
		shape := sourceError == nil && output != nil && modelShape(modelType, output)
		// Implicit discovery deliberately ignores unrelated returns, unlike
		// C# static-method discovery. Explicit queries still diagnose them.
		if d.query && (!validResults || !shape) || source && (!validResults || !shape) {
			report(decl.Name, "ARC0001", "query must return (owning-model shape, error), optionally in an admitted observable source")
		}
		if shape && (sig.TypeParams().Len() > 0 || sig.RecvTypeParams().Len() > 0) {
			report(decl.Name, "ARC0014", "query cannot be generic; use a concrete declaration or move the helper outside query discovery")
		}
	}
	return nil, nil
}

func diagnosticDeclarationType(t types.Type) types.Type {
	t = diagnosticBaseType(t)
	if named, ok := t.(*types.Named); ok {
		return named.Origin()
	}
	return t
}

func diagnosticBaseType(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	t = types.Unalias(t)
	if pointer, ok := t.(*types.Pointer); ok {
		return types.Unalias(pointer.Elem())
	}
	return t
}

func commandLikeType(t types.Type) bool {
	structure, ok := t.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := range structure.NumFields() {
		if structure.Field(i).Exported() && !structure.Field(i).Embedded() {
			return true
		}
	}
	return false
}

func diagnosticQueryConvention(decl *ast.FuncDecl) bool {
	if !decl.Name.IsExported() || decl.Recv == nil || len(decl.Recv.List) != 1 {
		return false
	}
	receiver := decl.Recv.List[0]
	if _, pointer := receiver.Type.(*ast.StarExpr); pointer {
		return false
	}
	return len(receiver.Names) == 0 || len(receiver.Names) == 1 && receiver.Names[0].Name == "_"
}

func diagnosticOutput(sig *types.Signature) (types.Type, bool) {
	results := sig.Results()
	if results.Len() == 0 || types.Identical(results.At(0).Type(), types.Universe.Lookup("error").Type()) {
		return nil, false
	}
	return results.At(0).Type(), results.Len() == 2 && types.Identical(results.At(1).Type(), types.Universe.Lookup("error").Type())
}

func reportUnusedPreparation(pass *goanalysis.Pass, handle, provide *ast.FuncDecl, report func(ast.Node, string, string)) {
	provided, valid := diagnosticOutput(pass.TypesInfo.Defs[provide.Name].Type().(*types.Signature))
	if !valid {
		return // Invalid signatures remain arc-gen's responsibility.
	}
	payload, control := preparationType(provided)
	if control != "" && control != "preparation" {
		return
	}
	sig := pass.TypesInfo.Defs[handle.Name].Type().(*types.Signature)
	for i := range sig.Params().Len() {
		if types.Identical(sig.Params().At(i).Type(), payload) {
			return // Arc uses exact identity, not C#'s implicit assignability.
		}
	}
	report(provide.Name, "ARC0005", fmt.Sprintf("Provide payload %s is not consumed by Handle; add an exact-type parameter or return a control-only result", payload))
}

func reportAuthorizationConflict(group *ast.CommentGroup, report func(ast.Node, string, string)) {
	if group == nil {
		return
	}
	var anonymous *ast.Comment
	restricted := false
	for _, comment := range group.List {
		words := strings.Fields(strings.TrimSpace(strings.TrimPrefix(comment.Text, "//")))
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "arc:allow-anonymous":
			anonymous = comment
		case "arc:authorize":
			restricted = true
		}
	}
	if anonymous != nil && restricted {
		// Point at the marker, not the entire comment including its //.
		node := &ast.Ident{NamePos: anonymous.Pos() + token.Pos(strings.Index(anonymous.Text, "arc:")), Name: "arc:allow-anonymous"}
		report(node, "ARC0019", "allow-anonymous conflicts with authorize on this declaration; keep one authorization level")
	}
}
