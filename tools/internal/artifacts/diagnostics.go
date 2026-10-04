// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/ast"
	"go/types"
	"reflect"
	"strings"

	goanalysis "golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

// AuthoringAnalyzer checks admitted, opted-in query bodies without executing
// application code or generating adapters. It shares arc-gen's declaration
// admission and Fundamentals' concept classifier; it is not a runtime validator.
var AuthoringAnalyzer = &goanalysis.Analyzer{
	Name: "arcauthoring",
	Doc:  "report ARC0015 when a primitive query argument field is converted directly to a concept",
	Run:  runAuthoringDiagnostics,
}

const queryArgumentConceptDiagnostic = "ARC0015"

func runAuthoringDiagnostics(pass *goanalysis.Pass) (any, error) {
	// Use the driver's already type-checked package. Do not load it again, strip
	// generated symbols from its scope, or mutate the shared syntax/type graph.
	pkg := &packages.Package{
		Fset: pass.Fset, Types: pass.Pkg, TypesInfo: pass.TypesInfo, TypesSizes: pass.TypesSizes,
	}
	for _, file := range pass.Files {
		if !ast.IsGenerated(file) {
			pkg.Syntax = append(pkg.Syntax, file)
		}
	}
	a, err := analyzeDeclarations(pkg, true)
	if err != nil {
		return nil, err
	}
	var diagnostics []goanalysis.Diagnostic
	for _, q := range a.queries {
		found, err := queryArgumentDiagnostics(pkg, q)
		if err != nil {
			return nil, err
		}
		diagnostics = append(diagnostics, found...)
	}
	for _, finding := range diagnostics {
		pass.Report(finding)
	}
	return nil, nil
}

func queryArgumentDiagnostics(pkg *packages.Package, q query) ([]goanalysis.Diagnostic, error) {
	decl := q.call.decl
	if decl.Body == nil || q.args == nil {
		return nil, nil
	}
	sig := pkg.TypesInfo.Defs[decl.Name].Type().(*types.Signature)
	var argument *types.Var
	for i, parameter := range q.call.params {
		if parameter.kind == "arguments" {
			argument = sig.Params().At(i)
			break
		}
	}
	var diagnostics []goanalysis.Diagnostic
	var failure error
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		if failure != nil {
			return false
		}
		if _, closure := node.(*ast.FuncLit); closure {
			return false // No interprocedural or closure-capture analysis.
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || !pkg.TypesInfo.Types[call.Fun].IsType() {
			return true
		}
		selector, ok := ast.Unparen(call.Args[0]).(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := ast.Unparen(selector.X).(*ast.Ident)
		if !ok || pkg.TypesInfo.Uses[receiver] != argument {
			return true
		}
		selection := pkg.TypesInfo.Selections[selector]
		if selection == nil || selection.Kind() != types.FieldVal || len(selection.Index()) != 1 {
			return true
		}
		field := selection.Obj().(*types.Var)
		if !field.Exported() || field.Anonymous() {
			return true
		}
		structure := types.Unalias(argument.Type()).Underlying().(*types.Struct)
		jsonName := strings.SplitN(reflect.StructTag(structure.Tag(selection.Index()[0])).Get("json"), ",", 2)[0]
		if jsonName == "-" {
			return true
		}
		// Aliases of built-in scalars remain primitive. Distinct named scalars,
		// pointers, nested fields, and UUID representations are outside this rule.
		primitive, ok := types.Unalias(field.Type()).(*types.Basic)
		if !ok || primitive.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat|types.IsString) == 0 {
			return true
		}
		target := pkg.TypesInfo.TypeOf(call)
		_, recognized, err := conceptWire(target)
		if err != nil {
			failure = diagnostic(pkg, call.Pos(), "%v", err)
			return false
		}
		if !recognized {
			return true
		}
		diagnostics = append(diagnostics, goanalysis.Diagnostic{
			Pos: selector.Sel.Pos(), End: selector.Sel.End(), Category: queryArgumentConceptDiagnostic,
			Message: fmt.Sprintf("%s: query argument %s.%s is declared as %s and converted to %s in the body; declare the argument field as the concept so input concept validation can run",
				queryArgumentConceptDiagnostic, argument.Name(), field.Name(), field.Type(), target),
		})
		return true
	})
	return diagnostics, failure
}
