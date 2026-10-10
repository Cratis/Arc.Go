// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"

	"github.com/cratis/arc.go/metadata"
	"golang.org/x/tools/go/packages"
)

func analyzeWireGraph(graph *Graph, analyses []*analysis, profile ApplicationProfile, typescript bool) error {
	w := &wireAnalyzer{graph: graph, profile: profile, packages: map[string]*packages.Package{}, namespaces: map[string]string{}, declarations: map[string]*model{}, nodes: map[string]*TypeDescriptor{}, compilerTypes: map[string]types.Type{}, interfaceModels: map[string]*model{}, typescript: typescript, typescriptRequested: typescript, contract: graph.FormatVersion == ContractGraphVersion, seenTypes: map[string]bool{}}
	if len(analyses) > 0 {
		w.sizes = analyses[0].pkg.TypesSizes
	}
	// V2 normalizes runtime contracts first. TS admission is a separate walk
	// of the same graph, rooted only in the selected TS families.
	if w.contract {
		w.typescript = false
	}
	w.indexAnalyses(analyses)
	if err := w.collectDependencyDeclarations(); err != nil {
		return err
	}
	if err := w.indexInterfaceModels(); err != nil {
		return err
	}
	for _, a := range analyses {
		if err := w.describeAnalysisRoots(a); err != nil {
			return err
		}
	}
	if err := w.describeProfileRoots(); err != nil {
		return err
	}
	return w.finishGraph(analyses)
}

func (w *wireAnalyzer) indexPackage(pkg *packages.Package) {
	if pkg == nil || w.packages[pkg.PkgPath] != nil {
		return
	}
	w.packages[pkg.PkgPath] = pkg
	for _, imported := range pkg.Imports {
		w.indexPackage(imported)
	}
}

func (w *wireAnalyzer) indexAnalyses(analyses []*analysis) {
	for _, a := range analyses {
		w.indexPackage(a.pkg)
		w.namespaces[a.pkg.PkgPath] = a.namespace
		for i := range a.commands {
			w.declarations[typeKey(a.commands[i].typ)] = &a.commands[i].model
		}
		for _, models := range [][]*model{a.models, a.exports, a.enums} {
			for _, model := range models {
				w.declarations[typeKey(model.typ)] = model
			}
		}
	}
}

// collectDependencyDeclarations records dependency declarations as compiler
// metadata, never runtime discovery.
func (w *wireAnalyzer) collectDependencyDeclarations() error {
	paths := make([]string, 0, len(w.packages))
	for path := range w.packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		pkg := w.packages[path]
		for _, file := range pkg.Syntax {
			if err := w.collectFileDeclarations(pkg, path, file); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *wireAnalyzer) collectFileDeclarations(pkg *packages.Package, path string, file *ast.File) error {
	if file.Doc != nil {
		d, err := parseDirectives(file.Doc)
		if err != nil {
			return diagnostic(pkg, file.Doc.Pos(), "%v", err)
		}
		if d.hasNamespace {
			if _, selected := w.namespaces[path]; !selected {
				w.namespaces[path] = d.namespace
			}
		}
	}
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if err := w.collectTypeSpecDeclaration(pkg, gen, typeSpec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *wireAnalyzer) collectTypeSpecDeclaration(pkg *packages.Package, gen *ast.GenDecl, typeSpec *ast.TypeSpec) error {
	group := typeSpec.Doc
	if group == nil && len(gen.Specs) == 1 {
		group = gen.Doc
	}
	d, err := parseDirectives(group)
	if err != nil {
		return diagnostic(pkg, typeSpec.Pos(), "%v", err)
	}
	if d.kind != "enum" && d.kind != "model" {
		return nil
	}
	object := pkg.Types.Scope().Lookup(typeSpec.Name.Name)
	if object == nil {
		return nil
	}
	named, ok := types.Unalias(object.Type()).(*types.Named)
	if !ok || d.ignore {
		return nil
	}
	key := typeKey(named)
	if w.declarations[key] == nil {
		if d.name == "" {
			d.name = named.Obj().Name()
		}
		w.declarations[key] = &model{typ: named, d: d, pos: typeSpec.Pos()}
	}
	return nil
}

func (w *wireAnalyzer) indexInterfaceModels() error {
	for _, key := range sortedKeys(w.declarations) {
		model := w.declarations[key]
		if model.d.targetInterface == "" || model.d.derivedID != "" {
			continue
		}
		base, err := w.reference(model.typ.Obj().Pkg(), model.d.targetInterface)
		if err != nil {
			return err
		}
		if previous := w.interfaceModels[typeKey(base)]; previous != nil && previous != model {
			return w.fail(model.typ, "duplicate default model for interface %s", base)
		}
		w.interfaceModels[typeKey(base)] = model
	}
	return nil
}

func (w *wireAnalyzer) describeAnalysisRoots(a *analysis) error {
	for i := range a.commands {
		if err := w.describeCommand(a, &a.commands[i]); err != nil {
			return err
		}
	}
	for i := range a.queries {
		if err := w.describeQuery(a, &a.queries[i]); err != nil {
			return err
		}
	}
	return w.describeExportRoots(a)
}

func (w *wireAnalyzer) describeCommand(a *analysis, command *command) error {
	descriptor := command.descriptor
	if descriptor.Excluded && w.profile.OpenAPI == nil {
		return nil
	}
	input, err := w.describe(command.typ)
	if err != nil {
		return err
	}
	descriptor.Fields = w.nodes[input.Target].Fields
	if w.contract {
		descriptor.Input = &input
	}
	if !descriptor.Excluded {
		w.tsRoots = append(w.tsRoots, input)
	}
	for _, field := range descriptor.Fields {
		if field.TagRules() {
			descriptor.PortableRules = true
		}
	}
	output := command.handle.output
	override := command.d.response
	if configured, exists := w.profile.Responses[descriptor.Declaration.Type.Identity()]; exists {
		override = configured
	}
	if override == "none" {
		descriptor.ResponseKind = "none"
		return nil
	}
	if output == nil {
		return nil
	}
	return w.describeCommandResponse(a, command, output, override)
}

func (w *wireAnalyzer) describeCommandResponse(a *analysis, command *command, output types.Type, override string) error {
	descriptor := command.descriptor
	profile := w.profile
	if namedType(output, runtimePath+"/commands", "Outcome") {
		named := types.Unalias(output).(*types.Named)
		if named.TypeArgs().Len() != 1 {
			return w.fail(output, "invalid framework response wrapper")
		}
		output = named.TypeArgs().At(0)
		if namedType(output, runtimePath+"/commands", "NoResponse") {
			descriptor.ResponseKind = "none"
		} else {
			descriptor.ResponseKind = "value"
		}
	} else if override == "value" {
		descriptor.ResponseKind = "value"
	} else if override != "" {
		return w.fail(output, "response override must be none or value")
	}
	ambiguous := descriptor.ResponseKind == "unknown"
	if ambiguous && w.contract {
		if field, exists := profile.ResponseFields[descriptor.Declaration.Type.Identity()]["response"]; exists {
			if field.Absent {
				descriptor.ResponseKind = "none"
			} else {
				descriptor.ResponseKind = "value"
			}
		}
	}
	if descriptor.ResponseKind == "unknown" {
		return diagnostic(a.pkg, command.handle.decl.Pos(), "client response is ambiguous; use commands.Outcome[T] or an explicit response=none/value contract")
	}
	if descriptor.ResponseKind != "value" {
		return nil
	}
	response, err := w.describe(output)
	if field, exists := profile.ResponseFields[descriptor.Declaration.Type.Identity()]["response"]; w.contract && ambiguous && exists && !field.Absent {
		response, err = w.declaredResponse(field)
	}
	if err != nil {
		return err
	}
	descriptor.Response = &response
	if !descriptor.Excluded {
		w.tsRoots = append(w.tsRoots, response)
	}
	return nil
}

func (w *wireAnalyzer) describeQuery(a *analysis, query *query) error {
	descriptor := query.descriptor
	if descriptor.Excluded && w.profile.OpenAPI == nil {
		return nil
	}
	result, err := w.describe(query.model.typ)
	if err != nil {
		return err
	}
	descriptor.Result = result
	descriptor.Result.Nullable = query.shape.nullable
	descriptor.Paged = query.shape.paged
	if query.shape.collection {
		element, err := w.describe(query.shape.element)
		if err != nil {
			return err
		}
		descriptor.Result = WireType{Kind: "array", Element: &element}
	}
	if w.contract {
		if err := w.describeQueryContract(query); err != nil {
			return err
		}
	}
	if !descriptor.Excluded {
		w.tsRoots = append(w.tsRoots, descriptor.Result)
	}
	if query.emission != nil && query.shape.collection {
		if err := validateObservableGoIdentity(query.model.typ); err != nil {
			return diagnostic(a.pkg, query.call.decl.Pos(), "%v", err)
		}
	}
	for _, field := range w.nodes[result.Target].Fields {
		if field.Identity {
			descriptor.Declaration.ReadModelIdentityMember = field.Name
		}
		if field.Sortable {
			descriptor.SortFields = append(descriptor.SortFields, field.Name)
		}
	}
	if query.args != nil && !namedType(query.args, runtimePath+"/queries", "NoArguments") {
		return w.describeQueryArguments(query)
	}
	return nil
}

func (w *wireAnalyzer) describeQueryContract(query *query) error {
	descriptor := query.descriptor
	descriptor.DataPresence = "omit-nil-data"
	if !query.shape.collection {
		return nil
	}
	data, err := w.describe(types.NewSlice(query.shape.element))
	if err != nil {
		return err
	}
	declared := query.call.output
	if query.emission != nil {
		declared = query.emission
	}
	data.Contract.Declared = typeKey(declared)
	if array, ok := types.Unalias(declared).Underlying().(*types.Array); ok {
		length := array.Len()
		data.Contract.FixedLength = &length
		data.Contract.InputNull = false
		data.Contract.OutputNull = false
	}
	descriptor.Result.Contract = data.Contract
	return nil
}

func (w *wireAnalyzer) describeQueryArguments(query *query) error {
	descriptor := query.descriptor
	fields, err := w.fields(query.args, true)
	if err != nil {
		return err
	}
	descriptor.Parameters = fields
	if !descriptor.Excluded {
		for _, field := range fields {
			if w.typescriptRequested && w.contract {
				if field.Binding.PreservePresence {
					return w.fail(query.args, "query preservePresence requires an explicit presence-compatible client contract")
				}
				if field.Name == "constructor" || field.Name == "__proto__" || field.Name == "prototype" {
					return w.fail(query.args, "reserved frontend wire field %q", field.Name)
				}
			}
			w.tsRoots = append(w.tsRoots, field.Type)
		}
	}
	for _, field := range fields {
		if field.TagRules() {
			descriptor.PortableRules = true
		}
	}
	return nil
}

func (w *wireAnalyzer) describeExportRoots(a *analysis) error {
	for _, roots := range [][]*model{a.exports, a.enums} {
		for _, root := range roots {
			excluded := excluded(w.profile, (metadata.TypeName{Namespace: a.namespace, Name: root.d.name}).Identity())
			if w.profile.OpenAPI != nil || !excluded {
				wire, err := w.describe(root.typ)
				if err != nil {
					return err
				}
				if !excluded {
					w.tsRoots = append(w.tsRoots, wire)
				}
			}
		}
	}
	return nil
}

func (w *wireAnalyzer) describeProfileRoots() error {
	for _, key := range w.profile.TypeRoots {
		t, err := w.reference(nil, key)
		if err != nil {
			return err
		}
		wire, err := w.describe(t)
		if err != nil {
			return err
		}
		w.tsRoots = append(w.tsRoots, wire)
	}
	return nil
}

// describeResponseFields describes every declared response field and returns
// the validation state schema when one is declared.
func (w *wireAnalyzer) describeResponseFields() (*WireType, error) {
	var validationState *WireType
	for _, identity := range sortedKeys(w.profile.ResponseFields) {
		for _, path := range sortedKeys(w.profile.ResponseFields[identity]) {
			field := w.profile.ResponseFields[identity][path]
			if field.Absent {
				continue
			}
			wire, err := w.declaredResponse(field)
			if err != nil {
				return nil, fmt.Errorf("responseFields %s.%s: %w", identity, path, err)
			}
			if identity == "Cratis.ValidationResult" && path == "state" {
				validationState = &wire
			}
		}
	}
	return validationState, nil
}

func (w *wireAnalyzer) finishGraph(analyses []*analysis) error {
	graph, profile := w.graph, w.profile
	validationState, err := w.describeResponseFields()
	if err != nil {
		return err
	}
	if profile.OpenAPI != nil {
		framework, err := frameworkContracts(graph, validationState)
		if err != nil {
			return err
		}
		graph.Framework = framework
	}
	if profile.Server != nil && profile.Server.Identity.DetailsType != "" {
		t, err := w.reference(nil, profile.Server.Identity.DetailsType)
		if err != nil {
			return err
		}
		if _, err := w.describe(t); err != nil {
			return err
		}
	}
	for _, key := range sortedKeys(w.nodes) {
		graph.Types = append(graph.Types, *w.nodes[key])
	}
	sort.Strings(graph.Diagnostics)
	if err := w.validateGraph(); err != nil {
		return err
	}
	for _, a := range analyses {
		a.wireTypes = w.compilerTypes
	}
	return nil
}

func (w *wireAnalyzer) validateGraph() error {
	graph := w.graph
	if w.contract {
		if err := validateContractDerivatives(graph.Types); err != nil {
			return err
		}
	}
	if w.contract && w.typescriptRequested {
		if err := w.validateTypeScriptContract(); err != nil {
			return err
		}
	} else if w.typescript {
		if err := rejectConstructorCycles(graph.Types); err != nil {
			return err
		}
	}
	for _, key := range sortedKeys(w.profile.WireSchemas) {
		if !w.seenTypes[key] {
			return fmt.Errorf("unknown or unreachable wireSchemas type reference %q", key)
		}
	}
	return nil
}
