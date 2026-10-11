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
	if err := w.collectFileNamespace(pkg, path, file); err != nil {
		return err
	}
	for _, declaration := range file.Decls {
		if err := w.collectDeclaration(pkg, declaration); err != nil {
			return err
		}
	}
	return nil
}

func (w *wireAnalyzer) collectFileNamespace(pkg *packages.Package, path string, file *ast.File) error {
	if file.Doc == nil {
		return nil
	}
	d, err := parseDirectives(file.Doc)
	if err != nil {
		return diagnostic(pkg, file.Doc.Pos(), "%v", err)
	}
	if !d.hasNamespace {
		return nil
	}
	if _, selected := w.namespaces[path]; !selected {
		w.namespaces[path] = d.namespace
	}
	return nil
}

func (w *wireAnalyzer) collectDeclaration(pkg *packages.Package, declaration ast.Decl) error {
	gen, ok := declaration.(*ast.GenDecl)
	if !ok {
		return nil
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
	return nil
}

func typeSpecDirectives(pkg *packages.Package, gen *ast.GenDecl, typeSpec *ast.TypeSpec) (directives, error) {
	group := typeSpec.Doc
	if group == nil && len(gen.Specs) == 1 {
		group = gen.Doc
	}
	d, err := parseDirectives(group)
	if err != nil {
		return d, diagnostic(pkg, typeSpec.Pos(), "%v", err)
	}
	return d, nil
}

func declaredNamedType(pkg *packages.Package, typeSpec *ast.TypeSpec) *types.Named {
	object := pkg.Types.Scope().Lookup(typeSpec.Name.Name)
	if object == nil {
		return nil
	}
	named, _ := types.Unalias(object.Type()).(*types.Named)
	return named
}

func (w *wireAnalyzer) collectTypeSpecDeclaration(pkg *packages.Package, gen *ast.GenDecl, typeSpec *ast.TypeSpec) error {
	d, err := typeSpecDirectives(pkg, gen, typeSpec)
	if err != nil {
		return err
	}
	if d.kind != "enum" && d.kind != "model" {
		return nil
	}
	named := declaredNamedType(pkg, typeSpec)
	if named == nil || d.ignore {
		return nil
	}
	key := typeKey(named)
	if w.declarations[key] != nil {
		return nil
	}
	if d.name == "" {
		d.name = named.Obj().Name()
	}
	w.declarations[key] = &model{typ: named, d: d, pos: typeSpec.Pos()}
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
	if err := w.describeCommandRoots(a); err != nil {
		return err
	}
	if err := w.describeQueryRoots(a); err != nil {
		return err
	}
	return w.describeExportRoots(a)
}

func (w *wireAnalyzer) describeCommandRoots(a *analysis) error {
	for i := range a.commands {
		if err := w.describeCommand(a, &a.commands[i]); err != nil {
			return err
		}
	}
	return nil
}

func (w *wireAnalyzer) describeQueryRoots(a *analysis) error {
	for i := range a.queries {
		if err := w.describeQuery(a, &a.queries[i]); err != nil {
			return err
		}
	}
	return nil
}

func anyTagRules(fields []FieldDescriptor) bool {
	for _, field := range fields {
		if field.TagRules() {
			return true
		}
	}
	return false
}

func (w *wireAnalyzer) describeCommand(a *analysis, command *command) error {
	descriptor := command.descriptor
	if descriptor.Excluded && w.profile.OpenAPI == nil {
		return nil
	}
	if err := w.describeCommandInput(command); err != nil {
		return err
	}
	override := w.commandResponseOverride(command)
	if override == "none" {
		descriptor.ResponseKind = "none"
		return nil
	}
	output := command.handle.output
	if output == nil {
		return nil
	}
	return w.describeCommandResponse(a, command, output, override)
}

func (w *wireAnalyzer) describeCommandInput(command *command) error {
	descriptor := command.descriptor
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
	if anyTagRules(descriptor.Fields) {
		descriptor.PortableRules = true
	}
	return nil
}

func (w *wireAnalyzer) commandResponseOverride(command *command) string {
	if configured, exists := w.profile.Responses[command.descriptor.Declaration.Type.Identity()]; exists {
		return configured
	}
	return command.d.response
}

// unwrapCommandResponse sets the response kind from the handler output and
// returns the type that carries the response value.
func (w *wireAnalyzer) unwrapCommandResponse(descriptor *CommandDescriptor, output types.Type, override string) (types.Type, error) {
	if namedType(output, runtimePath+"/commands", "Outcome") {
		named := types.Unalias(output).(*types.Named)
		if named.TypeArgs().Len() != 1 {
			return nil, w.fail(output, "invalid framework response wrapper")
		}
		output = named.TypeArgs().At(0)
		descriptor.ResponseKind = "value"
		if namedType(output, runtimePath+"/commands", "NoResponse") {
			descriptor.ResponseKind = "none"
		}
		return output, nil
	}
	switch override {
	case "value":
		descriptor.ResponseKind = "value"
	case "":
	default:
		return nil, w.fail(output, "response override must be none or value")
	}
	return output, nil
}

// declaredResponseField returns the profile-declared client response field
// for the command when one is present.
func (w *wireAnalyzer) declaredResponseField(descriptor *CommandDescriptor) (ResponseField, bool) {
	field, exists := w.profile.ResponseFields[descriptor.Declaration.Type.Identity()]["response"]
	return field, exists
}

// resolveAmbiguousResponse settles an unknown response kind from the profile
// declaration in contract mode and returns the declared field that supplies
// the response value, or nil when the declaration does not apply.
func (w *wireAnalyzer) resolveAmbiguousResponse(descriptor *CommandDescriptor) *ResponseField {
	if descriptor.ResponseKind != "unknown" || !w.contract {
		return nil
	}
	field, exists := w.declaredResponseField(descriptor)
	if !exists {
		return nil
	}
	if field.Absent {
		descriptor.ResponseKind = "none"
		return nil
	}
	descriptor.ResponseKind = "value"
	return &field
}

func (w *wireAnalyzer) describeResponseValue(output types.Type, declared *ResponseField) (WireType, error) {
	response, err := w.describe(output)
	if declared != nil {
		return w.declaredResponse(*declared)
	}
	return response, err
}

func (w *wireAnalyzer) describeCommandResponse(a *analysis, command *command, output types.Type, override string) error {
	descriptor := command.descriptor
	output, err := w.unwrapCommandResponse(descriptor, output, override)
	if err != nil {
		return err
	}
	declared := w.resolveAmbiguousResponse(descriptor)
	if descriptor.ResponseKind == "unknown" {
		return diagnostic(a.pkg, command.handle.decl.Pos(), "client response is ambiguous; use commands.Outcome[T] or an explicit response=none/value contract")
	}
	if descriptor.ResponseKind != "value" {
		return nil
	}
	response, err := w.describeResponseValue(output, declared)
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
	result, err := w.describeQueryResult(query)
	if err != nil {
		return err
	}
	if err := w.describeQueryProjection(query); err != nil {
		return err
	}
	if err := validateQueryEmission(a, query); err != nil {
		return err
	}
	w.collectQueryResultMembers(query, result)
	if query.args != nil && !namedType(query.args, runtimePath+"/queries", "NoArguments") {
		return w.describeQueryArguments(query)
	}
	return nil
}

func (w *wireAnalyzer) describeQueryProjection(query *query) error {
	if w.contract {
		if err := w.describeQueryContract(query); err != nil {
			return err
		}
	}
	if !query.descriptor.Excluded {
		w.tsRoots = append(w.tsRoots, query.descriptor.Result)
	}
	return nil
}

func (w *wireAnalyzer) describeQueryResult(query *query) (WireType, error) {
	descriptor := query.descriptor
	result, err := w.describe(query.model.typ)
	if err != nil {
		return result, err
	}
	descriptor.Result = result
	descriptor.Result.Nullable = query.shape.nullable
	descriptor.Paged = query.shape.paged
	if query.shape.collection {
		element, err := w.describe(query.shape.element)
		if err != nil {
			return result, err
		}
		descriptor.Result = WireType{Kind: "array", Element: &element}
	}
	return result, nil
}

func validateQueryEmission(a *analysis, query *query) error {
	if query.emission == nil || !query.shape.collection {
		return nil
	}
	if err := validateObservableGoIdentity(query.model.typ); err != nil {
		return diagnostic(a.pkg, query.call.decl.Pos(), "%v", err)
	}
	return nil
}

func (w *wireAnalyzer) collectQueryResultMembers(query *query, result WireType) {
	descriptor := query.descriptor
	for _, field := range w.nodes[result.Target].Fields {
		if field.Identity {
			descriptor.Declaration.ReadModelIdentityMember = field.Name
		}
		if field.Sortable {
			descriptor.SortFields = append(descriptor.SortFields, field.Name)
		}
	}
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
		if err := w.addQueryArgumentRoots(query, fields); err != nil {
			return err
		}
	}
	if anyTagRules(fields) {
		descriptor.PortableRules = true
	}
	return nil
}

func isReservedFrontendField(name string) bool {
	return name == "constructor" || name == "__proto__" || name == "prototype"
}

func (w *wireAnalyzer) validateQueryArgumentField(query *query, field FieldDescriptor) error {
	if !w.typescriptRequested || !w.contract {
		return nil
	}
	if field.Binding.PreservePresence {
		return w.fail(query.args, "query preservePresence requires an explicit presence-compatible client contract")
	}
	if isReservedFrontendField(field.Name) {
		return w.fail(query.args, "reserved frontend wire field %q", field.Name)
	}
	return nil
}

func (w *wireAnalyzer) addQueryArgumentRoots(query *query, fields []FieldDescriptor) error {
	for _, field := range fields {
		if err := w.validateQueryArgumentField(query, field); err != nil {
			return err
		}
		w.tsRoots = append(w.tsRoots, field.Type)
	}
	return nil
}

func (w *wireAnalyzer) describeExportRoots(a *analysis) error {
	for _, roots := range [][]*model{a.exports, a.enums} {
		for _, root := range roots {
			if err := w.describeExportRoot(a, root); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *wireAnalyzer) describeExportRoot(a *analysis, root *model) error {
	excluded := excluded(w.profile, (metadata.TypeName{Namespace: a.namespace, Name: root.d.name}).Identity())
	if w.profile.OpenAPI == nil && excluded {
		return nil
	}
	wire, err := w.describe(root.typ)
	if err != nil {
		return err
	}
	if !excluded {
		w.tsRoots = append(w.tsRoots, wire)
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
	graph := w.graph
	validationState, err := w.describeResponseFields()
	if err != nil {
		return err
	}
	if w.profile.OpenAPI != nil {
		framework, err := frameworkContracts(graph, validationState)
		if err != nil {
			return err
		}
		graph.Framework = framework
	}
	if err := w.describeIdentityDetails(); err != nil {
		return err
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

func (w *wireAnalyzer) describeIdentityDetails() error {
	server := w.profile.Server
	if server == nil || server.Identity.DetailsType == "" {
		return nil
	}
	t, err := w.reference(nil, server.Identity.DetailsType)
	if err != nil {
		return err
	}
	_, err = w.describe(t)
	return err
}

func (w *wireAnalyzer) validateGraph() error {
	graph := w.graph
	if w.contract {
		if err := validateContractDerivatives(graph.Types); err != nil {
			return err
		}
	}
	if err := w.validateTypeScriptWalk(); err != nil {
		return err
	}
	return w.validateWireSchemas()
}

func (w *wireAnalyzer) validateTypeScriptWalk() error {
	if w.contract && w.typescriptRequested {
		return w.validateTypeScriptContract()
	}
	if w.typescript {
		return rejectConstructorCycles(w.graph.Types)
	}
	return nil
}

func (w *wireAnalyzer) validateWireSchemas() error {
	for _, key := range sortedKeys(w.profile.WireSchemas) {
		if !w.seenTypes[key] {
			return fmt.Errorf("unknown or unreachable wireSchemas type reference %q", key)
		}
	}
	return nil
}
