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
	"slices"
	"sort"
	"strconv"
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
	graph               *Graph
	profile             ApplicationProfile
	packages            map[string]*packages.Package
	namespaces          map[string]string
	declarations        map[string]*model
	nodes               map[string]*TypeDescriptor
	compilerTypes       map[string]types.Type
	interfaceModels     map[string]*model
	typescript          bool
	typescriptRequested bool
	contract            bool
	sizes               types.Sizes
	seenTypes           map[string]bool
	tsRoots             []WireType
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

// ownsEnumParser admits only the method from the generator's owned file, not
// an application codec that happens to accompany an enum directive.
func (w *wireAnalyzer) ownsEnumParser(named *types.Named, members map[string]int32) (bool, error) {
	pkg := w.packages[named.Obj().Pkg().Path()]
	if pkg == nil || pkg.Fset == nil {
		return false, nil
	}
	for i := range named.NumMethods() {
		method := named.Method(i)
		if method.Name() != "UnmarshalJSON" {
			continue
		}
		path := pkg.Fset.Position(method.Pos()).Filename
		if filepath.Base(path) != Filename {
			return false, nil
		}
		for _, file := range pkg.Syntax {
			if pkg.Fset.Position(file.Pos()).Filename != path || len(file.Comments) == 0 || len(file.Comments[0].List) == 0 || !owned([]byte(file.Comments[0].List[0].Text)) {
				continue
			}
			if !enumParserMatches(pkg, file, named, members) {
				return false, fmt.Errorf("imported enum parser for %s is stale; run arc-gen on %s", named.Obj().Name(), pkg.PkgPath)
			}
			return true, nil
		}
	}
	return false, nil
}

// enumParserMatches checks the generated parse map against current declarations.
// Constant references (rather than copied values) keep changed numeric values
// current, but added/removed names must be regenerated in the dependency package.
func enumParserMatches(pkg *packages.Package, file *ast.File, named *types.Named, members map[string]int32) bool {
	candidates, matches := 0, 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		function, ok := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
		if !ok || function.Pkg() == nil || function.Pkg().Path() != runtimePath+"/serialization" || function.Name() != "NewInt32Enum" {
			return true
		}
		literal, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		mapType, ok := pkg.TypesInfo.TypeOf(literal).(*types.Map)
		if !ok || !types.Identical(mapType.Key(), types.Typ[types.String]) || !types.Identical(mapType.Elem(), named) {
			return true
		}
		candidates++
		if len(literal.Elts) != len(members) {
			return false
		}
		seen := map[string]bool{}
		for _, element := range literal.Elts {
			entry, ok := element.(*ast.KeyValueExpr)
			if !ok {
				return false
			}
			key, ok := entry.Key.(*ast.BasicLit)
			if !ok || key.Kind != token.STRING {
				return false
			}
			name, err := strconv.Unquote(key.Value)
			if err != nil || seen[name] {
				return false
			}
			value, exists := members[name]
			identifier, ok := entry.Value.(*ast.Ident)
			if !exists || !ok || identifier.Name != name {
				return false
			}
			constantObject, ok := pkg.TypesInfo.Uses[identifier].(*types.Const)
			if !ok || constantObject != named.Obj().Pkg().Scope().Lookup(name) {
				return false
			}
			number, fits := constant.Int64Val(constantObject.Val())
			if !fits || number != int64(value) {
				return false
			}
			seen[name] = true
		}
		matches++
		return false
	})
	return candidates == 1 && matches == 1
}

func (w *wireAnalyzer) describeValue(t types.Type) (WireType, error) {
	// The traversal hook owns runtime output before scalar/struct inference,
	// including application concepts. Keep declared schemas and import mappings
	// on the existing named-type path instead of treating a marker as evidence.
	if !slices.Contains(codecMethodNames(t), "MarshalJSONWith") {
		if wire, recognized, err := conceptWire(t); err != nil {
			return WireType{}, fmt.Errorf("wire type %s: %w", typeKey(t), err)
		} else if recognized {
			if w.typescript && wire.Kind == "number" {
				w.graph.Diagnostics = append(w.graph.Diagnostics, "number precision: concept "+typeKey(t)+" uses JavaScript number; enforce safe range in domain validation")
			}
			return wire, nil
		}
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
		declaration := w.declarations[key]
		if methods := codecMethodNames(named); len(methods) > 0 {
			if len(methods) == 1 && methods[0] == "UnmarshalJSON" && declaration != nil && declaration.d.kind == "enum" && declaration.d.parse == "int32" {
				members, err := int32EnumDeclarationMembers(declaration)
				if err != nil {
					return WireType{}, w.fail(t, "%v", err)
				}
				owned, err := w.ownsEnumParser(named, members)
				if err != nil {
					return WireType{}, w.fail(t, "%v", err)
				}
				if owned {
					return w.enum(declaration)
				}
			}
			if slices.Contains(methods, "MarshalJSONWith") {
				return WireType{}, w.fail(t, "opaque custom codec requires an explicit wire import mapping: %s implements MarshalJSONWith (or declare explicit input/output schemas with contract-v2 wireSchemas)", key)
			}
			return WireType{}, w.fail(t, "opaque custom codec requires an explicit wire import mapping")
		}
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
			presence := &FieldPresence{InputMissing: "zero", InputNull: wire.Contract.InputNull, OutputNull: wire.Contract.OutputNull, OmitNil: nilableGo(member.Type), OmitMissing: wire.Kind == "optional" && wire.Contract.PointerDepth <= 1, OmitEmpty: member.OmitEmpty && emptyOmission(member.Type), OmitZero: member.OmitZero, EmbeddedParentNullable: parentNullable}
			if wire.Kind == "optional" && wire.Contract.PointerDepth == 0 {
				presence.InputMissing = "missing"
			}
			if member.OmitZero && hasMethod(member.Type, "IsZero") && wire.Kind != "optional" && wire.Kind != "Date" {
				return nil, w.fail(t, "field %s dynamic omitzero predicate requires a containing-type output schema declaration", member.Name)
			}
			presence.OutputRequired = !presence.OmitNil && !presence.OmitMissing && !presence.OmitEmpty && !presence.OmitZero && !parentNullable
			if presence.OmitNil {
				// Only immediate nil is omitted, not a declared codec's encoded
				// null. Nonnil pointers can also encode inner nil or Optional null.
				presence.OutputNull = wire.Contract.Schemas != nil && schemaAcceptsNull(wire.Contract.Schemas.Output)
				if pointer, ok := types.Unalias(member.Type).Underlying().(*types.Pointer); ok {
					element, err := w.describe(pointer.Elem())
					if err != nil {
						return nil, fmt.Errorf("wire field %s.%s: %w", typeKey(t), member.Name, err)
					}
					presence.OutputNull = element.Contract.OutputNull
				}
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
			if len(field.Rules) > 0 {
				field.QueryRules = queryRuleRepresentation(member.Type)
			}
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
		if model.d.parse == "int32" {
			// Numeric input is closed to declared values, while strings also
			// accept names, OR combinations and any Int32 decimal spelling.
			// Do not project this parser as an ordinary open integer schema.
			node.EnumDomain = "int32-parser"
		}
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
