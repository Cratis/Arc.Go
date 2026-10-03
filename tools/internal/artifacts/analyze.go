// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package artifacts analyzes explicitly selected packages and emits typed Arc adapters.
package artifacts

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sort"

	"github.com/cratis/arc.go/metadata"
	"golang.org/x/tools/go/packages"
)

const runtimePath = "github.com/cratis/arc.go"

type model struct {
	typ *types.Named
	d   directives
	pos token.Pos
}

type parameter struct {
	typ  types.Type
	kind string
}

type method struct {
	decl   *ast.FuncDecl
	params []parameter
	output types.Type // nil means error-only
}

type command struct {
	descriptor *CommandDescriptor
	model
	receiver types.Type
	handle   method
	provide  *method
	payload  types.Type
	control  string
}

type query struct {
	descriptor *QueryDescriptor
	model      *model
	d          directives
	call       method
	args       types.Type // nil means queries.NoArguments
	emission   types.Type // nonnil only for declared observable sources
	shape      queryShape
}

type analysis struct {
	pkg             *packages.Package
	namespace       string
	commands        []command
	models          []*model
	queries         []query
	exports         []*model
	enums           []*model
	graph           *Graph
	staticNamespace bool
	wireTypes       map[string]types.Type
}

func diagnostic(pkg *packages.Package, pos token.Pos, format string, args ...any) error {
	return fmt.Errorf("%s: %s", pkg.Fset.Position(pos), fmt.Sprintf(format, args...))
}

func analyze(pkg *packages.Package) (*analysis, error) {
	a := &analysis{pkg: pkg}
	docs := map[*ast.CommentGroup]directives{}
	for _, file := range pkg.Syntax {
		for _, group := range file.Comments {
			d, err := parseDirectives(group)
			if err != nil {
				pos := d.pos
				if !pos.IsValid() {
					pos = group.Pos()
				}
				return nil, diagnostic(pkg, pos, "%v", err)
			}
			if d.pos.IsValid() {
				docs[group] = d
			}
		}
	}
	typesByName := map[string]*model{}
	methods := map[string]map[string]*ast.FuncDecl{}
	var functions []*ast.FuncDecl
	namespaceSet := false
	for _, file := range pkg.Syntax {
		if d, ok := docs[file.Doc]; ok {
			if !d.hasNamespace || d.kind != "" || d.auth != nil || d.exclude || d.ignore || namespaceSet {
				return nil, diagnostic(pkg, d.pos, "package documentation permits one arc:namespace only")
			}
			a.namespace, namespaceSet = d.namespace, true
			delete(docs, file.Doc)
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
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
					d, marked := docs[group]
					if !marked {
						continue
					}
					delete(docs, group)
					if d.ignore {
						continue
					}
					if d.hasNamespace || d.kind != "command" && d.kind != "readmodel" && d.kind != "enum" && d.kind != "model" {
						return nil, diagnostic(pkg, ts.Pos(), "type directives require arc:command or arc:readmodel")
					}
					named, ok := pkg.TypesInfo.Defs[ts.Name].Type().(*types.Named)
					if !ok || ts.Assign.IsValid() || named.TypeParams().Len() > 0 {
						return nil, diagnostic(pkg, ts.Pos(), "artifacts require a defined nongeneric struct")
					}
					if d.kind == "enum" {
						if d.name == "" {
							d.name = named.Obj().Name()
						}
						a.enums = append(a.enums, &model{typ: named, d: d, pos: ts.Pos()})
						continue
					}
					if d.kind == "model" {
						if d.name == "" {
							d.name = named.Obj().Name()
						}
						a.exports = append(a.exports, &model{typ: named, d: d, pos: ts.Pos()})
						continue
					}
					st, ok := named.Underlying().(*types.Struct)
					if !ok {
						return nil, diagnostic(pkg, ts.Pos(), "artifacts require a struct")
					}
					for i := range st.NumFields() {
						if st.Field(i).Name() != "_" {
							continue
						}
						tag := reflect.StructTag(st.Tag(i))
						_, arcTag := tag.Lookup("arc")
						_, authTag := tag.Lookup("authorize")
						if arcTag || authTag {
							return nil, diagnostic(pkg, st.Field(i).Pos(), "declaration tags and artifact directives cannot be combined")
						}
					}
					if d.name == "" {
						d.name = named.Obj().Name()
					}
					m := &model{typ: named, d: d, pos: ts.Pos()}
					typesByName[ts.Name.Name] = m
				}
			case *ast.FuncDecl:
				functions = append(functions, decl)
				if decl.Recv != nil {
					if docs[decl.Doc].ignore && decl.Name.Name != "Validate" {
						continue
					}
					sig := pkg.TypesInfo.Defs[decl.Name].Type().(*types.Signature)
					t := sig.Recv().Type()
					if ptr, ok := t.(*types.Pointer); ok {
						t = ptr.Elem()
					}
					named, ok := t.(*types.Named)
					if !ok {
						continue
					}
					name := named.Obj().Name()
					if methods[name] == nil {
						methods[name] = map[string]*ast.FuncDecl{}
					}
					methods[name][decl.Name.Name] = decl
				}
			}
		}
	}
	names := make([]string, 0, len(typesByName))
	for name := range typesByName {
		names = append(names, name)
	}
	sort.Strings(names)
	identities := map[string]bool{}
	for _, name := range names {
		m := typesByName[name]
		if err := (metadata.Model{Kind: metadata.ModelKind(m.d.kind), Type: metadata.TypeName{Namespace: a.namespace, Name: m.d.name}, Path: m.d.path, Authorization: m.d.auth, BlockOnValidationSeverity: m.d.severity}).Validate(); err != nil {
			return nil, diagnostic(pkg, m.pos, "invalid model metadata: %v", err)
		}
		identity := m.d.kind + ":" + m.d.name
		if identities[identity] {
			return nil, diagnostic(pkg, m.pos, "duplicate artifact identity %q", m.d.name)
		}
		identities[identity] = true
		if m.d.kind == "readmodel" {
			a.models = append(a.models, m)
			continue
		}
		c, err := analyzeCommand(pkg, m, methods[name])
		if err != nil {
			return nil, err
		}
		a.commands = append(a.commands, c)
	}
	for _, decl := range functions {
		d, explicit := docs[decl.Doc]
		delete(docs, decl.Doc)
		if d.ignore {
			if decl.Recv != nil && decl.Name.Name == "Validate" {
				return nil, diagnostic(pkg, decl.Pos(), "arc:ignore cannot disable runtime model validation; use explicit manual registration options")
			}
			continue
		}
		if explicit && (d.hasNamespace || d.kind != "" && d.kind != "query") {
			return nil, diagnostic(pkg, decl.Pos(), "function directives require arc:query")
		}
		var owner *model
		if decl.Recv != nil {
			sig := pkg.TypesInfo.Defs[decl.Name].Type().(*types.Signature)
			receiver, valueReceiver := sig.Recv().Type().(*types.Named)
			if !valueReceiver {
				if explicit {
					return nil, diagnostic(pkg, decl.Pos(), "query requires an unnamed value receiver")
				}
				continue
			}
			owner = typesByName[receiver.Obj().Name()]
			unnamed := len(decl.Recv.List[0].Names) == 0 || decl.Recv.List[0].Names[0].Name == "_"
			if owner == nil || owner.d.kind != "readmodel" || !unnamed {
				if explicit {
					return nil, diagnostic(pkg, decl.Pos(), "query requires an unnamed value receiver on an arc:readmodel")
				}
				continue
			}
			if d.model != "" && d.model != receiver.Obj().Name() {
				return nil, diagnostic(pkg, decl.Pos(), "query model conflicts with receiver")
			}
			if !explicit && (!decl.Name.IsExported() || sig.Results().Len() == 0 || !queryCandidate(owner.typ, sig.Results().At(0).Type())) {
				continue
			}
		} else if explicit {
			owner = typesByName[d.model]
			if d.kind != "query" || owner == nil || owner.d.kind != "readmodel" {
				return nil, diagnostic(pkg, decl.Pos(), "arc:query requires model=<local arc:readmodel type>")
			}
		} else {
			continue
		}
		q, err := analyzeQuery(pkg, owner, d, decl)
		if err != nil {
			return nil, err
		}
		identity := "query:" + owner.d.name + "." + q.d.name
		if identities[identity] {
			return nil, diagnostic(pkg, decl.Pos(), "duplicate query identity %q", identity)
		}
		identities[identity] = true
		a.queries = append(a.queries, q)
	}
	for _, file := range pkg.Syntax {
		for _, group := range file.Comments {
			if d, exists := docs[group]; exists {
				return nil, diagnostic(pkg, d.pos, "misplaced arc directive")
			}
		}
	}
	if len(a.commands)+len(a.models) > 0 {
		for _, name := range []string{"RegisterArtifacts", "ArcBindings"} {
			if obj := pkg.Types.Scope().Lookup(name); obj != nil {
				return nil, diagnostic(pkg, obj.Pos(), "%s is reserved for generated adapters; keep composition in another package", name)
			}
		}
	}
	sort.Slice(a.queries, func(i, j int) bool {
		return a.queries[i].model.d.name+"."+a.queries[i].d.name < a.queries[j].model.d.name+"."+a.queries[j].d.name
	})
	return a, nil
}

func analyzeCommand(pkg *packages.Package, m *model, methods map[string]*ast.FuncDecl) (command, error) {
	c := command{model: *m}
	handle := methods["Handle"]
	if handle == nil {
		return c, diagnostic(pkg, m.pos, "command requires a declared Handle method (promoted methods are not discovered)")
	}
	sig := pkg.TypesInfo.Defs[handle.Name].Type().(*types.Signature)
	c.receiver = sig.Recv().Type()
	var err error
	c.handle, err = analyzeMethod(pkg, handle, "command", true)
	if err != nil {
		return c, err
	}
	if err := checkValidate(pkg, m.typ, c.receiver); err != nil {
		return c, err
	}
	if provide := methods["Provide"]; provide != nil {
		p, err := analyzeMethod(pkg, provide, "command", false)
		if err != nil {
			return c, err
		}
		ps := pkg.TypesInfo.Defs[provide.Name].Type().(*types.Signature)
		if _, ptr := ps.Recv().Type().(*types.Pointer); ptr && types.Identical(c.receiver, m.typ) {
			return c, diagnostic(pkg, provide.Pos(), "pointer Provide cannot prepare a value-registered command")
		}
		c.provide = &p
		c.payload, c.control = preparationType(p.output)
		if c.control == "" || c.control == "preparation" {
			matches := 0
			for i := range c.handle.params {
				if types.Identical(c.handle.params[i].typ, c.payload) {
					if c.handle.params[i].kind != "dependency" {
						return c, diagnostic(pkg, handle.Pos(), "provided payload conflicts with an infrastructure parameter")
					}
					c.handle.params[i].kind = "provided"
					matches++
				}
			}
			if matches != 1 {
				return c, diagnostic(pkg, handle.Pos(), "Provide payload must match exactly one Handle parameter; found %d", matches)
			}
		}
	}
	return c, nil
}

func analyzeMethod(pkg *packages.Package, decl *ast.FuncDecl, kind string, allowVoid bool) (method, error) {
	m := method{decl: decl}
	sig := pkg.TypesInfo.Defs[decl.Name].Type().(*types.Signature)
	if sig.Variadic() || sig.TypeParams().Len() > 0 || sig.RecvTypeParams().Len() > 0 {
		return m, diagnostic(pkg, decl.Pos(), "variadic or generic artifact methods are unsupported")
	}
	results := sig.Results()
	isError := func(t types.Type) bool { return types.Identical(t, types.Universe.Lookup("error").Type()) }
	if results.Len() == 1 && isError(results.At(0).Type()) && allowVoid {
		// Error-only Handle is a void command.
	} else if results.Len() == 2 && isError(results.At(1).Type()) {
		m.output = results.At(0).Type()
	} else {
		return m, diagnostic(pkg, decl.Pos(), "unsupported results: expected (T, error)%s", map[bool]string{true: " or error"}[allowVoid])
	}
	seen := map[string]bool{}
	for i := range sig.Params().Len() {
		t := sig.Params().At(i).Type()
		role := "dependency"
		switch {
		case namedType(t, "context", "Context"):
			role = "context"
		case namedType(t, runtimePath+"/commands", "CommandContext") && kind == "command":
			role = "commandContext"
		case namedType(t, runtimePath+"/queries", "QueryContext") && kind == "query":
			role = "queryContext"
		case namedType(t, runtimePath+"/queries", "Parameters") && kind == "query":
			role = "parameters"
		}
		if role != "dependency" && seen[role] {
			return m, diagnostic(pkg, decl.Pos(), "duplicate infrastructure parameter %s", role)
		}
		seen[role] = true
		m.params = append(m.params, parameter{typ: t, kind: role})
	}
	return m, nil
}

func analyzeQuery(pkg *packages.Package, owner *model, d directives, decl *ast.FuncDecl) (query, error) {
	q := query{model: owner, d: d}
	if q.d.name == "" {
		q.d.name = decl.Name.Name
	}
	m, err := analyzeMethod(pkg, decl, "query", false)
	if err != nil {
		return q, err
	}
	output := m.output
	if emission, recognized, err := sourceEmission(output); recognized {
		if err != nil {
			return q, diagnostic(pkg, decl.Pos(), "%v", err)
		}
		q.emission, output = emission, emission
	}
	var supported bool
	q.shape, supported = classifyQueryShape(owner.typ, output)
	if !supported {
		return q, diagnostic(pkg, decl.Pos(), "query must return or emit its owning model, pointer, slice, array, value queries.Page or queries.ObservedCollection")
	}
	for i := range m.params {
		if m.params[i].kind != "dependency" {
			continue
		}
		t := m.params[i].typ
		named, ok := types.Unalias(t).(*types.Named)
		if !ok {
			return q, diagnostic(pkg, decl.Pos(), "first ordinary query parameter must be a named argument struct; use queries.NoArguments before services")
		}
		if _, ok := named.Underlying().(*types.Struct); !ok {
			return q, diagnostic(pkg, decl.Pos(), "query arguments must be a named struct; use queries.NoArguments before services")
		}
		q.args = t
		m.params[i].kind = "arguments"
		if err := checkValidate(pkg, named, t); err != nil {
			return q, err
		}
		break
	}
	q.call = m
	if err := (metadata.Model{Type: metadata.TypeName{Name: q.d.name}, Path: d.path, Authorization: d.auth}).Validate(); err != nil {
		return q, diagnostic(pkg, decl.Pos(), "invalid query metadata: %v", err)
	}
	return q, nil
}

func checkValidate(pkg *packages.Package, named *types.Named, receiver types.Type) error {
	for i := range named.NumMethods() {
		fn := named.Method(i)
		if fn.Name() != "Validate" {
			continue
		}
		sig := fn.Type().(*types.Signature)
		bad := sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 2
		if !bad {
			slice, ok := sig.Results().At(0).Type().(*types.Slice)
			bad = !namedType(sig.Params().At(0).Type(), "context", "Context") || !ok || !namedType(slice.Elem(), runtimePath+"/validation", "Result") || !types.Identical(sig.Results().At(1).Type(), types.Universe.Lookup("error").Type())
		}
		_, methodPointer := sig.Recv().Type().(*types.Pointer)
		_, registeredPointer := receiver.(*types.Pointer)
		if bad || methodPointer && !registeredPointer {
			return diagnostic(pkg, fn.Pos(), "Validate must match the registered receiver and Validate(context.Context) ([]validation.Result, error)")
		}
	}
	return nil
}

func namedType(t types.Type, path, name string) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == path && n.Obj().Name() == name
}

func modelShape(model, output types.Type) bool {
	_, supported := classifyQueryShape(model, output)
	return supported
}

func preparationType(t types.Type) (types.Type, string) {
	if namedType(t, runtimePath+"/commands", "Preparation") {
		return types.Unalias(t).(*types.Named).TypeArgs().At(0), "preparation"
	}
	if namedType(t, runtimePath+"/validation", "Result") {
		return nil, "validation"
	}
	if s, ok := types.Unalias(t).(*types.Slice); ok && namedType(s.Elem(), runtimePath+"/validation", "Result") {
		return nil, "validations"
	}
	if namedType(t, runtimePath+"/authorization", "Decision") {
		return nil, "authorization"
	}
	if namedType(t, runtimePath+"/commands", "Result") {
		n := types.Unalias(t).(*types.Named)
		if n.TypeArgs().Len() == 1 && namedType(n.TypeArgs().At(0), runtimePath+"/commands", "NoResponse") {
			return nil, "result"
		}
	}
	return t, ""
}
