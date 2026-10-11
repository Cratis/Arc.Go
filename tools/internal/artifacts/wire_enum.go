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
	"sort"
	"strconv"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/fundamentals.go/naming"
	"golang.org/x/tools/go/packages"
)

type enumEntry struct {
	value    int64
	position token.Pos
	name     string
	exact    string
}

func (w *wireAnalyzer) enum(model *model) (WireType, error) {
	key := typeKey(model.typ)
	if w.nodes[key] != nil {
		return WireType{Kind: "enum", Target: key}, nil
	}
	node, err := w.newEnumNode(model, key)
	if err != nil {
		return WireType{}, err
	}
	values, err := w.collectEnumEntries(model)
	if err != nil {
		return WireType{}, err
	}
	sortEnumEntries(values)
	if err := w.addEnumMembers(model, node, values); err != nil {
		return WireType{}, err
	}
	w.nodes[key] = node
	w.compilerTypes[key] = model.typ
	return WireType{Kind: "enum", Target: key}, nil
}

func (w *wireAnalyzer) newEnumNode(model *model, key string) (*TypeDescriptor, error) {
	base, ok := model.typ.Underlying().(*types.Basic)
	if !ok || base.Info()&types.IsInteger == 0 {
		return nil, w.fail(model.typ, "numeric enums require an integer underlying type")
	}
	namespace, exists := w.packageNamespace(model.typ.Obj().Pkg().Path())
	if !exists {
		return nil, w.fail(model.typ, "enum namespace must be explicitly mapped")
	}
	node := &TypeDescriptor{Key: key, Name: metadata.TypeName{Namespace: namespace, Name: model.d.name}, Kind: "enum", Flags: model.d.flags}
	if w.contract {
		if err := w.applyEnumContract(model, node, base); err != nil {
			return nil, err
		}
	}
	if pkg := w.packages[model.typ.Obj().Pkg().Path()]; pkg != nil {
		node.Source = filepath.Base(pkg.Fset.Position(model.pos).Filename)
	}
	return node, nil
}

func (w *wireAnalyzer) applyEnumContract(model *model, node *TypeDescriptor, base *types.Basic) error {
	scalar, err := scalarContract(base, w.sizes)
	if err != nil {
		return err
	}
	node.Scalar = scalar
	node.EnumDomain = "open-underlying-integer"
	if model.d.parse == "int32" {
		// Numeric input is closed to declared values, while strings also
		// accept names, OR combinations and any Int32 decimal spelling.
		// Do not project this parser as an ordinary open integer schema.
		node.EnumDomain = "int32-parser"
	}
	return nil
}

func (w *wireAnalyzer) collectEnumEntries(model *model) ([]enumEntry, error) {
	var values []enumEntry
	mapped := map[string]bool{}
	scope := model.typ.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		object, ok := scope.Lookup(name).(*types.Const)
		if !ok || !types.Identical(object.Type(), model.typ) {
			continue
		}
		entry, err := w.enumEntryFor(model, name, object, mapped)
		if err != nil {
			return nil, err
		}
		values = append(values, entry)
	}
	if len(values) == 0 || len(mapped) != len(model.d.members) {
		return nil, w.fail(model.typ, "enum requires typed constants and valid member mappings")
	}
	return values, nil
}

func outsideRange(value, minimum, maximum int64) bool {
	return value < minimum || value > maximum
}

func exceedsEnumLimits(value int64, fits, flags bool) bool {
	if !fits || outsideRange(value, -9007199254740991, 9007199254740991) {
		return true
	}
	return flags && outsideRange(value, -2147483648, 2147483647)
}

func (w *wireAnalyzer) enumEntryFor(model *model, name string, object *types.Const, mapped map[string]bool) (enumEntry, error) {
	value, ok := constant.Int64Val(object.Val())
	if w.typescript && exceedsEnumLimits(value, ok, model.d.flags) {
		return enumEntry{}, w.fail(model.typ, "enum value %s exceeds safe number/bitwise limits", object.Name())
	}
	member := name
	if override, exists := model.d.members[name]; exists {
		member = override
		mapped[name] = true
	}
	if !ok {
		unsigned, fits := constant.Uint64Val(object.Val())
		if !fits {
			return enumEntry{}, w.fail(model.typ, "invalid integer enum value %s", object.Name())
		}
		value = int64(unsigned)
	}
	return enumEntry{value, object.Pos(), naming.CamelCase(member), object.Val().ExactString()}, nil
}

func sortEnumEntries(values []enumEntry) {
	sort.Slice(values, func(i, j int) bool {
		if uint64(values[i].value) != uint64(values[j].value) {
			return uint64(values[i].value) < uint64(values[j].value)
		}
		return values[i].position < values[j].position
	})
}

func (w *wireAnalyzer) addEnumMembers(model *model, node *TypeDescriptor, values []enumEntry) error {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value.name] {
			return w.fail(model.typ, "duplicate enum export %s", value.name)
		}
		seen[value.name] = true
		node.Members = append(node.Members, EnumMember{Name: value.name, Value: value.exact})
	}
	return nil
}

// ownsEnumParser admits only the method from the generator's owned file, not
// an application codec that happens to accompany an enum directive.
func (w *wireAnalyzer) ownsEnumParser(named *types.Named, members map[string]int32) (bool, error) {
	pkg := w.packages[named.Obj().Pkg().Path()]
	if pkg == nil || pkg.Fset == nil {
		return false, nil
	}
	method := unmarshalJSONMethod(named)
	if method == nil {
		return false, nil
	}
	path := pkg.Fset.Position(method.Pos()).Filename
	if filepath.Base(path) != Filename {
		return false, nil
	}
	file := ownedGeneratedFile(pkg, path)
	if file == nil {
		return false, nil
	}
	if !enumParserMatches(pkg, file, named, members) {
		return false, fmt.Errorf("imported enum parser for %s is stale; run arc-gen on %s", named.Obj().Name(), pkg.PkgPath)
	}
	return true, nil
}

func unmarshalJSONMethod(named *types.Named) *types.Func {
	for i := range named.NumMethods() {
		if method := named.Method(i); method.Name() == "UnmarshalJSON" {
			return method
		}
	}
	return nil
}

func ownedGeneratedFile(pkg *packages.Package, path string) *ast.File {
	for _, file := range pkg.Syntax {
		if pkg.Fset.Position(file.Pos()).Filename == path && startsWithOwnedMarker(file) {
			return file
		}
	}
	return nil
}

func startsWithOwnedMarker(file *ast.File) bool {
	if len(file.Comments) == 0 || len(file.Comments[0].List) == 0 {
		return false
	}
	return owned([]byte(file.Comments[0].List[0].Text))
}

// enumParserCheck holds what a generated parse map is compared against.
type enumParserCheck struct {
	pkg     *packages.Package
	named   *types.Named
	members map[string]int32
}

// enumParserMatches checks the generated parse map against current declarations.
// Constant references (rather than copied values) keep changed numeric values
// current, but added/removed names must be regenerated in the dependency package.
func enumParserMatches(pkg *packages.Package, file *ast.File, named *types.Named, members map[string]int32) bool {
	check := enumParserCheck{pkg: pkg, named: named, members: members}
	candidates, matches := 0, 0
	ast.Inspect(file, func(node ast.Node) bool {
		literal := check.parserLiteral(node)
		if literal == nil {
			return true
		}
		candidates++
		if check.literalMatches(literal) {
			matches++
		}
		return false
	})
	return candidates == 1 && matches == 1
}

// parserLiteral returns the map literal passed to serialization.NewInt32Enum
// for the named enum, or nil when the node is not such a call.
func (c enumParserCheck) parserLiteral(node ast.Node) *ast.CompositeLit {
	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || !c.isNewInt32EnumCall(call) {
		return nil
	}
	literal, ok := call.Args[0].(*ast.CompositeLit)
	if !ok || !c.isEnumMap(literal) {
		return nil
	}
	return literal
}

func (c enumParserCheck) isNewInt32EnumCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	function, ok := c.pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
	return ok && function.Pkg() != nil && function.Pkg().Path() == runtimePath+"/serialization" && function.Name() == "NewInt32Enum"
}

func (c enumParserCheck) isEnumMap(literal *ast.CompositeLit) bool {
	mapType, ok := c.pkg.TypesInfo.TypeOf(literal).(*types.Map)
	return ok && types.Identical(mapType.Key(), types.Typ[types.String]) && types.Identical(mapType.Elem(), c.named)
}

func (c enumParserCheck) literalMatches(literal *ast.CompositeLit) bool {
	if len(literal.Elts) != len(c.members) {
		return false
	}
	seen := map[string]bool{}
	for _, element := range literal.Elts {
		if !c.entryMatches(element, seen) {
			return false
		}
	}
	return true
}

// entryName returns the unquoted string key of a map entry.
func entryName(entry *ast.KeyValueExpr) (string, bool) {
	key, ok := entry.Key.(*ast.BasicLit)
	if !ok || key.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(key.Value)
	return name, err == nil
}

func (c enumParserCheck) entryMatches(element ast.Expr, seen map[string]bool) bool {
	entry, ok := element.(*ast.KeyValueExpr)
	if !ok {
		return false
	}
	name, ok := entryName(entry)
	if !ok || seen[name] {
		return false
	}
	value, exists := c.members[name]
	identifier, ok := entry.Value.(*ast.Ident)
	if !exists || !ok || identifier.Name != name || !c.constantMatches(identifier, value) {
		return false
	}
	seen[name] = true
	return true
}

func (c enumParserCheck) constantMatches(identifier *ast.Ident, value int32) bool {
	constantObject, ok := c.pkg.TypesInfo.Uses[identifier].(*types.Const)
	if !ok || constantObject != c.named.Obj().Pkg().Scope().Lookup(identifier.Name) {
		return false
	}
	number, fits := constant.Int64Val(constantObject.Val())
	return fits && number == int64(value)
}
