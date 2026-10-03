// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
	"github.com/cratis/fundamentals.go/naming"
	"golang.org/x/tools/go/packages"
)

func compilerFields(t types.Type) ([]modelshape.WireField[types.Type], error) {
	return modelshape.Select(t, modelshape.Shape[types.Type]{
		Dereference: func(t types.Type) types.Type {
			t = types.Unalias(t)
			if pointer, ok := t.(*types.Pointer); ok {
				return types.Unalias(pointer.Elem())
			}
			return t
		},
		Members: func(t types.Type) ([]modelshape.Member[types.Type], bool) {
			st, ok := types.Unalias(t).Underlying().(*types.Struct)
			if !ok {
				return nil, false
			}
			members := make([]modelshape.Member[types.Type], st.NumFields())
			for i := range st.NumFields() {
				field := st.Field(i)
				members[i] = modelshape.Member[types.Type]{Name: field.Name(), Type: field.Type(), Tag: reflect.StructTag(st.Tag(i)), Exported: field.Exported(), Anonymous: field.Embedded()}
			}
			return members, true
		},
	})
}

type wireAnalyzer struct {
	graph           *Graph
	profile         ApplicationProfile
	packages        map[string]*packages.Package
	namespaces      map[string]string
	declarations    map[string]*model
	nodes           map[string]*TypeDescriptor
	compilerTypes   map[string]types.Type
	interfaceModels map[string]*model
	typescript      bool
	contract        bool
	sizes           types.Sizes
	seenTypes       map[string]bool
	tsRoots         []WireType
}

func analyzeWireGraph(graph *Graph, analyses []*analysis, profile ApplicationProfile, typescript bool) error {
	w := &wireAnalyzer{graph: graph, profile: profile, packages: map[string]*packages.Package{}, namespaces: map[string]string{}, declarations: map[string]*model{}, nodes: map[string]*TypeDescriptor{}, compilerTypes: map[string]types.Type{}, interfaceModels: map[string]*model{}, typescript: typescript, contract: graph.FormatVersion == ContractGraphVersion, seenTypes: map[string]bool{}}
	if len(analyses) > 0 {
		w.sizes = analyses[0].pkg.TypesSizes
	}
	// V2 normalizes runtime contracts first. TS admission is a separate walk
	// of the same graph, rooted only in the selected TS families.
	if w.contract {
		w.typescript = false
	}
	var indexPackage func(*packages.Package)
	indexPackage = func(pkg *packages.Package) {
		if pkg == nil || w.packages[pkg.PkgPath] != nil {
			return
		}
		w.packages[pkg.PkgPath] = pkg
		for _, imported := range pkg.Imports {
			indexPackage(imported)
		}
	}
	for _, a := range analyses {
		indexPackage(a.pkg)
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
	// Dependency declarations are compiler metadata, never runtime discovery.
	paths := make([]string, 0, len(w.packages))
	for path := range w.packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		pkg := w.packages[path]
		for _, file := range pkg.Syntax {
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
					group := typeSpec.Doc
					if group == nil && len(gen.Specs) == 1 {
						group = gen.Doc
					}
					d, err := parseDirectives(group)
					if err != nil {
						return diagnostic(pkg, typeSpec.Pos(), "%v", err)
					}
					if d.kind != "enum" && d.kind != "model" {
						continue
					}
					object := pkg.Types.Scope().Lookup(typeSpec.Name.Name)
					if object == nil {
						continue
					}
					named, ok := types.Unalias(object.Type()).(*types.Named)
					if !ok || d.ignore {
						continue
					}
					key := typeKey(named)
					if w.declarations[key] == nil {
						if d.name == "" {
							d.name = named.Obj().Name()
						}
						w.declarations[key] = &model{typ: named, d: d, pos: typeSpec.Pos()}
					}
				}
			}
		}
	}
	keys := make([]string, 0, len(w.declarations))
	for key := range w.declarations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
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
	for _, a := range analyses {
		for i := range a.commands {
			command := &a.commands[i]
			descriptor := command.descriptor
			if descriptor.Excluded && profile.OpenAPI == nil {
				continue
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
			if configured, exists := profile.Responses[descriptor.Declaration.Type.Identity()]; exists {
				override = configured
			}
			if override == "none" {
				descriptor.ResponseKind = "none"
			} else if output != nil {
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
				if descriptor.ResponseKind == "value" {
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
				}
			}
		}
		for i := range a.queries {
			query := &a.queries[i]
			descriptor := query.descriptor
			if descriptor.Excluded && profile.OpenAPI == nil {
				continue
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
				descriptor.DataPresence = "omit-nil-data"
				if query.shape.collection {
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
				fields, err := w.fields(query.args, true)
				if err != nil {
					return err
				}
				descriptor.Parameters = fields
				if !descriptor.Excluded {
					for _, field := range fields {
						if typescript && w.contract {
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
			}
		}
		for _, roots := range [][]*model{a.exports, a.enums} {
			for _, root := range roots {
				excluded := excluded(profile, (metadata.TypeName{Namespace: a.namespace, Name: root.d.name}).Identity())
				if profile.OpenAPI != nil || !excluded {
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
	}
	for _, key := range profile.TypeRoots {
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
	for _, identity := range sortedKeys(profile.ResponseFields) {
		for _, path := range sortedKeys(profile.ResponseFields[identity]) {
			field := profile.ResponseFields[identity][path]
			if field.Type != "" {
				t, err := w.reference(nil, field.Type)
				if err != nil {
					return fmt.Errorf("responseFields %s.%s: %w", identity, path, err)
				}
				if _, err := w.describe(t); err != nil {
					return err
				}
			}
		}
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
	keys = keys[:0]
	for key := range w.nodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		graph.Types = append(graph.Types, *w.nodes[key])
	}
	sort.Strings(graph.Diagnostics)
	if w.contract {
		if err := validateContractDerivatives(graph.Types); err != nil {
			return err
		}
	}
	if w.contract && typescript {
		if err := w.validateTypeScriptContract(); err != nil {
			return err
		}
	} else if w.typescript {
		if err := rejectConstructorCycles(graph.Types); err != nil {
			return err
		}
	}
	for _, key := range sortedKeys(profile.WireSchemas) {
		if !w.seenTypes[key] {
			return fmt.Errorf("unknown or unreachable wireSchemas type reference %q", key)
		}
	}
	for _, a := range analyses {
		a.wireTypes = w.compilerTypes
	}
	return nil
}

func (field FieldDescriptor) TagRules() bool {
	for _, rule := range field.Rules {
		if rule.Source == "explicit" {
			return true
		}
	}
	return false
}

func (w *wireAnalyzer) fail(t types.Type, format string, args ...any) error {
	if named, ok := types.Unalias(t).(*types.Named); ok {
		if pkg := w.packages[named.Obj().Pkg().Path()]; pkg != nil && pkg.Fset != nil {
			return diagnostic(pkg, named.Obj().Pos(), format, args...)
		}
	}
	return fmt.Errorf("wire type %s: %s", typeKey(t), fmt.Sprintf(format, args...))
}

func (w *wireAnalyzer) reference(pkg *types.Package, reference string) (types.Type, error) {
	if t := w.compilerTypes[reference]; t != nil {
		return t, nil
	}
	if pkg != nil {
		if object := pkg.Scope().Lookup(reference); object != nil {
			return object.Type(), nil
		}
		if alias, member, ok := strings.Cut(reference, "."); ok {
			if imported, ok := pkg.Scope().Lookup(alias).(*types.PkgName); ok {
				if object := imported.Imported().Scope().Lookup(member); object != nil {
					return object.Type(), nil
				}
			}
		}
	}
	for path, imported := range w.packages {
		if !strings.HasPrefix(reference, path+".") || imported.Types == nil {
			continue
		}
		if object := imported.Types.Scope().Lookup(strings.TrimPrefix(reference, path+".")); object != nil {
			return object.Type(), nil
		}
	}
	return nil, fmt.Errorf("unknown wire type reference %q", reference)
}

func (w *wireAnalyzer) describeValue(t types.Type) (WireType, error) {
	if wire, recognized, err := conceptWire(t); err != nil {
		return WireType{}, fmt.Errorf("wire type %s: %w", typeKey(t), err)
	} else if recognized {
		if w.typescript && wire.Kind == "number" {
			w.graph.Diagnostics = append(w.graph.Diagnostics, "number precision: concept "+typeKey(t)+" uses JavaScript number; enforce safe range in domain validation")
		}
		return wire, nil
	}
	t = types.Unalias(t)
	if pointer, ok := t.Underlying().(*types.Pointer); ok {
		wire, err := w.describe(pointer.Elem())
		wire.Nullable = true
		return wire, err
	}
	if namedType(t, "time", "Time") {
		return WireType{Kind: "Date"}, nil
	}
	if named, ok := t.(*types.Named); ok {
		if namedType(t, runtimePath+"/serialization", "Optional") {
			if w.typescript {
				return WireType{}, w.fail(t, "serialization.Optional requires a presence-compatible surface; strict C# proxy mode does not erase explicit null")
			}
			if named.TypeArgs().Len() != 1 {
				return WireType{}, w.fail(t, "invalid Optional type arguments")
			}
			element, err := w.describe(named.TypeArgs().At(0))
			return WireType{Kind: "optional", Element: &element, Nullable: true}, err
		}
		key := typeKey(t)
		if schemas, exists := w.profile.WireSchemas[key]; exists && !w.typescript {
			copy := schemas
			w.nodes[key] = &TypeDescriptor{Key: key, Name: metadata.TypeName{Name: named.Obj().Name()}, Kind: "declared", Schemas: &copy}
			w.compilerTypes[key] = t
			return WireType{Kind: "declared", Target: key}, nil
		}
		if mapped := w.profile.Imports[key]; mapped.Module != "" {
			if w.profile.OpenAPI != nil {
				if _, exists := w.profile.WireSchemas[key]; !exists {
					return WireType{}, w.fail(t, "opaque import requires explicit input/output wireSchemas; TS imports are not schema evidence")
				}
			}
			if w.nodes[key] == nil {
				mapping := mapped
				w.nodes[key] = &TypeDescriptor{Key: key, Name: metadata.TypeName{Name: mapped.Type}, Kind: "external", Import: &mapping}
			}
			w.compilerTypes[key] = t
			return WireType{Kind: "model", Target: key}, nil
		}
		if model := w.interfaceModels[key]; model != nil {
			return w.describe(model.typ)
		}
		for _, set := range []*types.MethodSet{types.NewMethodSet(named), types.NewMethodSet(types.NewPointer(named))} {
			for _, name := range []string{"MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText"} {
				if set.Lookup(nil, name) != nil {
					return WireType{}, w.fail(t, "opaque custom codec requires an explicit wire import mapping")
				}
			}
		}
		declaration := w.declarations[key]
		if declaration != nil && declaration.d.kind == "enum" {
			return w.enum(declaration)
		}
		if _, ok := named.Underlying().(*types.Struct); ok {
			if w.nodes[key] != nil {
				return WireType{Kind: "model", Target: key}, nil
			}
			namespace, exists := w.namespaces[named.Obj().Pkg().Path()]
			if configured, ok := w.profile.PackageNamespaces[named.Obj().Pkg().Path()]; ok {
				namespace, exists = configured, true
			}
			if !exists {
				return WireType{}, w.fail(t, "reachable external wire models require an explicit package namespace or import mapping")
			}
			name := named.Obj().Name()
			if declaration != nil {
				name = declaration.d.name
			}
			source := ""
			if pkg := w.packages[named.Obj().Pkg().Path()]; pkg != nil {
				source = filepath.Base(pkg.Fset.Position(named.Obj().Pos()).Filename)
			}
			node := &TypeDescriptor{Key: key, Name: metadata.TypeName{Namespace: namespace, Name: name}, Kind: "model", Source: source}
			w.nodes[key] = node
			w.compilerTypes[key] = t
			fields, err := w.fields(t, false)
			if err != nil {
				return WireType{}, err
			}
			node.Fields = fields
			if declaration != nil && declaration.d.targetInterface != "" {
				baseInterface, err := w.reference(named.Obj().Pkg(), declaration.d.targetInterface)
				if err != nil {
					return WireType{}, err
				}
				if _, ok := baseInterface.Underlying().(*types.Interface); !ok || !types.AssignableTo(named, baseInterface) && !types.AssignableTo(types.NewPointer(named), baseInterface) {
					return WireType{}, w.fail(t, "declared derivative must implement its interface")
				}
				if w.contract {
					if base, ok := types.Unalias(baseInterface).(*types.Named); !ok || base.Underlying().(*types.Interface).NumMethods() == 0 {
						return WireType{}, w.fail(t, "declared derivative requires a named nonempty interface")
					}
				}
				w.compilerTypes[typeKey(baseInterface)] = baseInterface
				node.Interface = typeKey(baseInterface)
				node.DerivedID = declaration.d.derivedID
				if w.contract {
					node.Discriminator = &DerivedContract{Property: "_derivedTypeId", Default: node.DerivedID == "", ID: node.DerivedID, OutputRequired: node.DerivedID != ""}
				}
				if declaration.d.derivedBase != "" {
					base, err := w.reference(named.Obj().Pkg(), declaration.d.derivedBase)
					if err != nil {
						return WireType{}, err
					}
					wire, err := w.describe(base)
					if err != nil {
						return WireType{}, err
					}
					baseNode := w.nodes[wire.Target]
					if baseNode == nil || baseNode.Interface != node.Interface {
						return WireType{}, w.fail(t, "derived model requires a matching declared base contract")
					}
					for _, inherited := range baseNode.Fields {
						found := false
						for _, field := range node.Fields {
							if field.Name == inherited.Name && reflect.DeepEqual(field.Type, inherited.Type) {
								found = true
							}
						}
						if !found {
							return WireType{}, w.fail(t, "derived model does not preserve base wire field %s", inherited.Name)
						}
					}
					node.Base = wire.Target
				}
			}
			return WireType{Kind: "model", Target: key}, nil
		}
		t = named.Underlying()
	}
	switch t := t.(type) {
	case *types.Basic:
		switch {
		case t.Info()&types.IsString != 0:
			return WireType{Kind: "string"}, nil
		case t.Info()&types.IsBoolean != 0:
			return WireType{Kind: "boolean"}, nil
		case t.Info()&(types.IsInteger|types.IsFloat) != 0:
			if w.typescript && (t.Kind() == types.Int64 || t.Kind() == types.Uint64 || t.Kind() == types.Int || t.Kind() == types.Uint) {
				w.graph.Diagnostics = append(w.graph.Diagnostics, "number precision: "+t.Name()+" values must remain within JavaScript's safe integer range")
			}
			return WireType{Kind: "number"}, nil
		}
	case *types.Slice:
		if basic, ok := types.Unalias(t.Elem()).Underlying().(*types.Basic); ok && basic.Kind() == types.Uint8 {
			return WireType{}, w.fail(t, "binary values require an explicit wire contract")
		}
		element, err := w.describe(t.Elem())
		return WireType{Kind: "array", Element: &element, Nullable: true}, err
	case *types.Array:
		if basic, ok := types.Unalias(t.Elem()).Underlying().(*types.Basic); ok && basic.Kind() == types.Uint8 && !w.contract {
			return WireType{}, w.fail(t, "binary arrays require an explicit wire contract (not inferred UUID)")
		}
		element, err := w.describe(t.Elem())
		return WireType{Kind: "array", Element: &element}, err
	case *types.Map:
		if basic, ok := types.Unalias(t.Key()).Underlying().(*types.Basic); !ok || basic.Kind() != types.String {
			return WireType{}, w.fail(t, "non-string dictionary keys require an explicit backend codec and wire mapping")
		}
		element, err := w.describe(t.Elem())
		if err != nil {
			return WireType{}, err
		}
		if w.typescript && element.Kind != "string" && element.Kind != "number" && element.Kind != "boolean" && element.Kind != "enum" {
			return WireType{}, w.fail(t, "rich dictionary values cannot hydrate through Object metadata; an explicit codec/mapping is required")
		}
		return WireType{Kind: "record", Element: &element, Nullable: true}, nil
	}
	return WireType{}, w.fail(t, "unsupported frontend wire type (no implicit any)")
}

func (w *wireAnalyzer) fields(t types.Type, arguments bool) ([]FieldDescriptor, error) {
	members, err := compilerFields(t)
	if err != nil {
		return nil, w.fail(t, "%v", err)
	}
	var fields []FieldDescriptor
	seenArgs := map[string]bool{}
	for _, member := range members {
		wire, err := w.describe(member.Type)
		if err != nil {
			return nil, fmt.Errorf("wire field %s.%s: %w", typeKey(t), member.Name, err)
		}
		field := FieldDescriptor{Name: member.Name, Type: wire, Optional: wire.Nullable || member.OmitEmpty || member.OmitZero, OmitEmpty: member.OmitEmpty, OmitZero: member.OmitZero}
		if w.contract {
			parentNullable := embeddedNullable(t, member.Index)
			presence := &FieldPresence{InputMissing: "zero", InputNull: wire.Contract.InputNull, OutputNull: wire.Contract.OutputNull, OmitNil: nilableGo(member.Type), OmitMissing: wire.Kind == "optional", OmitEmpty: member.OmitEmpty && emptyOmission(member.Type), OmitZero: member.OmitZero, EmbeddedParentNullable: parentNullable}
			if wire.Kind == "optional" && wire.Contract.PointerDepth == 0 {
				presence.InputMissing = "missing"
			}
			if member.OmitZero && hasMethod(member.Type, "IsZero") && wire.Kind != "optional" && wire.Kind != "Date" {
				return nil, w.fail(t, "field %s dynamic omitzero predicate requires a containing-type output schema declaration", member.Name)
			}
			presence.OutputRequired = !presence.OmitNil && !presence.OmitMissing && !presence.OmitEmpty && !presence.OmitZero && !parentNullable
			if presence.OmitNil {
				presence.OutputNull = false
			}
			field.Presence = presence
		}
		if w.typescript && (member.Name == "constructor" || member.Name == "__proto__" || member.Name == "prototype") {
			return nil, w.fail(t, "reserved frontend wire field %q", member.Name)
		}
		if value := member.Tag.Get("sortable"); value != "" {
			if value != "true" && value != "false" {
				return nil, w.fail(t, "sortable must be true or false")
			}
			field.Sortable = value == "true"
			if field.Sortable && (wire.Nullable || wire.Kind != "string" && wire.Kind != "number" && wire.Kind != "boolean" && wire.Kind != "enum") {
				return nil, w.fail(t, "sortable field %s requires a declared comparable scalar", member.Name)
			}
		}
		arcTags, err := modelshape.Options(member.Tag.Get("arc"))
		if err != nil {
			return nil, err
		}
		for _, tag := range arcTags {
			if tag.Name == "identity" {
				field.Identity = true
			}
		}
		kind := wire.Kind
		if kind == "enum" {
			kind = "number"
		}
		if kind == "array" {
			kind = "collection"
		}
		if kind != "string" && kind != "number" && kind != "boolean" && kind != "collection" {
			kind = "object"
		}
		rules, err := validation.ParseRules(member.Tag.Get("rules"), member.Name, kind)
		if err != nil {
			return nil, w.fail(t, "field %s: %v", member.Name, err)
		}
		field.Rules = rules
		tags, err := validation.ParseTags(member.Tag.Get("validate"))
		if err != nil {
			return nil, err
		}
		if tags.Required && len(rules) == 0 {
			name := "notNull"
			if wire.Kind == "string" {
				name = "notEmpty"
			}
			field.Rules = append(field.Rules, validation.RuleDescriptor{Property: member.Name, Name: name, Message: "The " + member.Name + " field is required.", Source: "annotation"})
		} else if tags.Required && len(rules) > 0 {
			return nil, w.fail(t, "field %s mixes required annotation with explicit rules; declare the presence rule explicitly", member.Name)
		}
		if arguments {
			key := strings.ToLower(member.Name)
			if seenArgs[key] {
				return nil, w.fail(t, "ambiguous query arguments %s", member.Name)
			}
			seenArgs[key] = true
			queryTags, err := metadata.ParseQueryTags(member.Tag.Get("query"))
			if err != nil {
				return nil, err
			}
			if w.typescript && queryTags.PreservePresence {
				return nil, w.fail(t, "query preservePresence requires an explicit presence-compatible client contract")
			}
			field.Required, field.HasDefault, field.Default = queryTags.Required, queryTags.HasDefault, queryTags.Default
			if w.contract {
				if err := validateBuiltinQueryType(member.Type, false); err != nil {
					return nil, w.fail(t, "query parameter %q: %v", member.Name, err)
				}
				missing := "zero"
				if field.Required {
					missing = "error"
				} else if field.HasDefault {
					missing = "default"
				}
				encoding := "scalar-text"
				queryType := unwrapOptional(member.Type)
				for {
					pointer, ok := types.Unalias(queryType).(*types.Pointer)
					if !ok {
						break
					}
					queryType = pointer.Elem()
				}
				var fixedLength *int64
				if !hasMethod(queryType, "UnmarshalText") {
					switch collection := queryType.Underlying().(type) {
					case *types.Array:
						encoding = "csv-or-json-array"
						length := collection.Len()
						fixedLength = &length
					case *types.Slice:
						encoding = "csv-or-json-array"
					}
				}
				field.Binding = &QueryBinding{Reader: "builtin", Encoding: encoding, CaseInsensitive: true, DuplicateNames: "reject-case-variants", RepeatedValues: "comma-join", EmptyAsMissing: !queryTags.PreservePresence, NullAsMissing: !queryTags.PreservePresence, PreservePresence: queryTags.PreservePresence, Missing: missing, FixedLength: fixedLength}
			}
			if field.HasDefault {
				var sizes types.Sizes
				if named, ok := types.Unalias(t).(*types.Named); ok {
					if pkg := w.packages[named.Obj().Pkg().Path()]; pkg != nil {
						sizes = pkg.TypesSizes
					}
				}
				if sizes == nil {
					return nil, w.fail(t, "query defaults require target compiler sizes")
				}
				defaultType := member.Type
				if w.contract {
					defaultType = unwrapOptional(defaultType)
				}
				if err := validateGoQueryDefault(defaultType, field.Default, sizes); err != nil {
					return nil, w.fail(t, "query parameter %q has unsupported server default: %v", member.Name, err)
				}
			}
			if key == "page" || key == "pagesize" || key == "sortby" || key == "sortdirection" || key == "waitforfirstresult" || key == "waitforfirstresulttimeout" {
				return nil, w.fail(t, "reserved query parameter %s", member.Name)
			}
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func (w *wireAnalyzer) enum(model *model) (WireType, error) {
	key := typeKey(model.typ)
	if w.nodes[key] != nil {
		return WireType{Kind: "enum", Target: key}, nil
	}
	base, ok := model.typ.Underlying().(*types.Basic)
	if !ok || base.Info()&types.IsInteger == 0 {
		return WireType{}, w.fail(model.typ, "numeric enums require an integer underlying type")
	}
	namespace, exists := w.namespaces[model.typ.Obj().Pkg().Path()]
	if configured, ok := w.profile.PackageNamespaces[model.typ.Obj().Pkg().Path()]; ok {
		namespace, exists = configured, true
	}
	if !exists {
		return WireType{}, w.fail(model.typ, "enum namespace must be explicitly mapped")
	}
	node := &TypeDescriptor{Key: key, Name: metadata.TypeName{Namespace: namespace, Name: model.d.name}, Kind: "enum", Flags: model.d.flags}
	if w.contract {
		scalar, err := scalarContract(base, w.sizes)
		if err != nil {
			return WireType{}, err
		}
		node.Scalar = scalar
		node.EnumDomain = "open-underlying-integer"
	}
	if pkg := w.packages[model.typ.Obj().Pkg().Path()]; pkg != nil {
		node.Source = filepath.Base(pkg.Fset.Position(model.pos).Filename)
	}
	type entry struct {
		value    int64
		position token.Pos
		name     string
		exact    string
	}
	var values []entry
	mapped := map[string]bool{}
	for _, name := range model.typ.Obj().Pkg().Scope().Names() {
		object, ok := model.typ.Obj().Pkg().Scope().Lookup(name).(*types.Const)
		if !ok || !types.Identical(object.Type(), model.typ) {
			continue
		}
		value, ok := constant.Int64Val(object.Val())
		if w.typescript && (!ok || value < -9007199254740991 || value > 9007199254740991 || model.d.flags && (value < -2147483648 || value > 2147483647)) {
			return WireType{}, w.fail(model.typ, "enum value %s exceeds safe number/bitwise limits", object.Name())
		}
		member := name
		if override, exists := model.d.members[name]; exists {
			member = override
			mapped[name] = true
		}
		if !ok {
			unsigned, fits := constant.Uint64Val(object.Val())
			if !fits {
				return WireType{}, w.fail(model.typ, "invalid integer enum value %s", object.Name())
			}
			value = int64(unsigned)
		}
		values = append(values, entry{value, object.Pos(), naming.CamelCase(member), object.Val().ExactString()})
	}
	if len(values) == 0 || len(mapped) != len(model.d.members) {
		return WireType{}, w.fail(model.typ, "enum requires typed constants and valid member mappings")
	}
	sort.Slice(values, func(i, j int) bool {
		if uint64(values[i].value) != uint64(values[j].value) {
			return uint64(values[i].value) < uint64(values[j].value)
		}
		return values[i].position < values[j].position
	})
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value.name] {
			return WireType{}, w.fail(model.typ, "duplicate enum export %s", value.name)
		}
		seen[value.name] = true
		node.Members = append(node.Members, EnumMember{Name: value.name, Value: value.exact})
	}
	w.nodes[key] = node
	w.compilerTypes[key] = model.typ
	return WireType{Kind: "enum", Target: key}, nil
}

func rejectConstructorCycles(nodes []TypeDescriptor) error {
	byKey := map[string]TypeDescriptor{}
	for _, node := range nodes {
		byKey[node.Key] = node
	}
	state := map[string]int{}
	var walk func(string, []string) error
	walk = func(key string, chain []string) error {
		if state[key] == 2 {
			return nil
		}
		if state[key] == 1 {
			return fmt.Errorf("eager constructor reference cycle: %s", strings.Join(append(chain, key), " -> "))
		}
		state[key] = 1
		node := byKey[key]
		if node.Base != "" {
			if err := walk(node.Base, append(chain, key)); err != nil {
				return err
			}
		}
		for _, field := range node.Fields {
			wire := field.Type
			for wire.Element != nil {
				wire = *wire.Element
			}
			if wire.Kind == "model" {
				if err := walk(wire.Target, append(chain, key)); err != nil {
					return err
				}
			}
		}
		state[key] = 2
		return nil
	}
	for _, node := range nodes {
		if err := walk(node.Key, nil); err != nil {
			return err
		}
	}
	return nil
}
