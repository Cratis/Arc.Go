// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"errors"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
)

// constructor is a package-level function whose result is registered lazily.
// Every non-context parameter is a dependency resolved like Handle dependencies.
type constructor struct {
	fn           *types.Func
	call         method
	result       types.Type
	returnsError bool
}

// validator is an arc:validator declaration. Exactly one of value and
// constructor is set: value registers one shared instance, constructor a
// scoped factory.
type validator struct {
	name        string
	pos         token.Pos
	model       types.Type
	concept     bool
	value       types.Type
	constructor *constructor
}

// policy is an arc:policy declaration with the same shared/scoped split.
type policy struct {
	name        string
	declaration string
	pos         token.Pos
	anonymous   bool
	value       types.Type
	constructor *constructor
}

const (
	validateSignature  = "Validate(context.Context, T) ([]validation.Result, error)"
	authorizeSignature = "Authorize(context.Context, authorization.Context) (authorization.Decision, error)"
)

// addRegisteredType records an arc:validator or arc:policy on a type
// declaration. The zero value of the struct is registered as a shared instance,
// by value when the value method set satisfies the contract, otherwise by pointer.
func (a *analysis) addRegisteredType(ts *ast.TypeSpec, d directives) error {
	pkg := a.pkg
	named, ok := pkg.TypesInfo.Defs[ts.Name].Type().(*types.Named)
	if !ok || ts.Assign.IsValid() || named.TypeParams().Len() > 0 {
		return diagnostic(pkg, ts.Pos(), "arc:%s requires a defined nongeneric struct type", d.kind)
	}
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return diagnostic(pkg, ts.Pos(), "arc:%s requires a struct type; use a constructor function for other types", d.kind)
	}
	value := types.Type(named)
	model, err := a.contract(d.kind, value)
	if err != nil {
		value = types.NewPointer(named)
		if pointerModel, pointerErr := a.contract(d.kind, value); pointerErr == nil {
			model, err = pointerModel, nil
		}
	}
	if err != nil {
		return diagnostic(pkg, ts.Pos(), "%s %s", named.Obj().Name(), err.Error())
	}
	return a.record(d, ts.Name.Name, ts.Pos(), model, value, nil)
}

// addConstructor records an arc:validator or arc:policy on a package-level
// constructor function, registered as a scoped factory.
func (a *analysis) addConstructor(decl *ast.FuncDecl, d directives) error {
	pkg := a.pkg
	if decl.Recv != nil {
		return diagnostic(pkg, decl.Pos(), "arc:%s requires a type declaration or a package-level constructor function, not a method", d.kind)
	}
	fn := pkg.TypesInfo.Defs[decl.Name].(*types.Func)
	sig := fn.Type().(*types.Signature)
	if sig.Variadic() || sig.TypeParams().Len() > 0 {
		return diagnostic(pkg, decl.Pos(), "variadic or generic arc:%s constructors are unsupported", d.kind)
	}
	results := sig.Results()
	errorType := types.Universe.Lookup("error").Type()
	c := &constructor{fn: fn, call: method{decl: decl}}
	switch {
	case results.Len() == 1 && !types.Identical(results.At(0).Type(), errorType):
		c.result = results.At(0).Type()
	case results.Len() == 2 && types.Identical(results.At(1).Type(), errorType):
		c.result, c.returnsError = results.At(0).Type(), true
	default:
		return diagnostic(pkg, decl.Pos(), "arc:%s constructor must return T or (T, error)", d.kind)
	}
	hasContext := false
	for i := range sig.Params().Len() {
		t := sig.Params().At(i).Type()
		role := "dependency"
		if namedType(t, "context", "Context") {
			if hasContext {
				return diagnostic(pkg, decl.Pos(), "duplicate infrastructure parameter context")
			}
			role, hasContext = "context", true
		}
		c.call.params = append(c.call.params, parameter{typ: t, kind: role})
	}
	model, err := a.contract(d.kind, c.result)
	if err != nil {
		return diagnostic(pkg, decl.Pos(), "%s result %s", decl.Name.Name, err.Error())
	}
	return a.record(d, decl.Name.Name, decl.Pos(), model, nil, c)
}

func (a *analysis) record(d directives, declaration string, pos token.Pos, model, value types.Type, c *constructor) error {
	if d.kind == "policy" {
		a.policies = append(a.policies, policy{name: d.name, declaration: declaration, pos: pos, anonymous: d.evaluatesAnonymous, value: value, constructor: c})
		return nil
	}
	if err := a.validatedModel(pos, model); err != nil {
		return err
	}
	a.validators = append(a.validators, validator{name: declaration, pos: pos, model: model, concept: d.concept, value: value, constructor: c})
	return nil
}

// contract returns the validated model for a validator, or nil for a policy,
// when t's method set satisfies the runtime interface exactly. Lookalike
// context, validation and authorization types never match.
func (a *analysis) contract(kind string, t types.Type) (types.Type, error) {
	name, signature := "Validate", validateSignature
	if kind == "policy" {
		name, signature = "Authorize", authorizeSignature
	}
	selection := types.NewMethodSet(t).Lookup(nil, name)
	if selection == nil {
		return nil, errors.New("must implement " + signature)
	}
	sig := selection.Obj().Type().(*types.Signature)
	params, results := sig.Params(), sig.Results()
	errorType := types.Universe.Lookup("error").Type()
	if sig.Variadic() || params.Len() != 2 || results.Len() != 2 || !namedType(params.At(0).Type(), "context", "Context") || !types.Identical(results.At(1).Type(), errorType) {
		return nil, errors.New("must implement " + signature)
	}
	if kind == "policy" {
		if !namedType(params.At(1).Type(), runtimePath+"/authorization", "Context") || !namedType(results.At(0).Type(), runtimePath+"/authorization", "Decision") {
			return nil, errors.New("must implement " + signature)
		}
		return nil, nil
	}
	slice, ok := types.Unalias(results.At(0).Type()).(*types.Slice)
	if !ok || !namedType(slice.Elem(), runtimePath+"/validation", "Result") {
		return nil, errors.New("must implement " + signature)
	}
	return params.At(1).Type(), nil
}

// validatedModel rejects models the runtime registry refuses and models the
// generated adapter cannot name from the declaring package.
func (a *analysis) validatedModel(pos token.Pos, model types.Type) error {
	switch types.Unalias(model).Underlying().(type) {
	case *types.Interface, *types.Signature, *types.Chan:
		return diagnostic(a.pkg, pos, "arc:validator model %s must be a concrete type, not an interface, function or channel", types.TypeString(model, nil))
	case *types.Basic:
		if basic := types.Unalias(model).Underlying().(*types.Basic); basic.Kind() == types.UnsafePointer {
			return diagnostic(a.pkg, pos, "arc:validator model must not be unsafe.Pointer")
		}
	}
	if !a.nameable(model, map[types.Type]bool{}) {
		return diagnostic(a.pkg, pos, "arc:validator model %s is not nameable from package %s", types.TypeString(model, nil), a.pkg.Name)
	}
	return nil
}

func (a *analysis) nameable(t types.Type, seen map[types.Type]bool) bool {
	if seen[t] {
		return true
	}
	seen[t] = true
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		if obj := t.Obj(); obj.Pkg() != nil && obj.Pkg() != a.pkg.Types && !obj.Exported() {
			return false
		}
		for i := range t.TypeArgs().Len() {
			if !a.nameable(t.TypeArgs().At(i), seen) {
				return false
			}
		}
		return true
	case *types.Pointer:
		return a.nameable(t.Elem(), seen)
	case *types.Slice:
		return a.nameable(t.Elem(), seen)
	case *types.Array:
		return a.nameable(t.Elem(), seen)
	case *types.Map:
		return a.nameable(t.Key(), seen) && a.nameable(t.Elem(), seen)
	case *types.Struct:
		for i := range t.NumFields() {
			if f := t.Field(i); !f.Exported() && f.Pkg() != a.pkg.Types || !a.nameable(f.Type(), seen) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// checkRegistrationIdentities rejects duplicates the runtime registries would
// reject at composition, and orders registrations deterministically.
func (a *analysis) checkRegistrationIdentities() error {
	key := func(t types.Type) string { return types.TypeString(t, nil) }
	sort.SliceStable(a.validators, func(i, j int) bool { return key(a.validators[i].model) < key(a.validators[j].model) })
	for i := 1; i < len(a.validators); i++ {
		if types.Identical(a.validators[i-1].model, a.validators[i].model) {
			return diagnostic(a.pkg, a.validators[i].pos, "duplicate arc:validator for %s; %s already validates it", key(a.validators[i].model), a.validators[i-1].name)
		}
	}
	sort.SliceStable(a.policies, func(i, j int) bool { return a.policies[i].name < a.policies[j].name })
	for i := 1; i < len(a.policies); i++ {
		if a.policies[i-1].name == a.policies[i].name {
			return diagnostic(a.pkg, a.policies[i].pos, "duplicate arc:policy name %q; %s already declares it", a.policies[i].name, a.policies[i-1].declaration)
		}
	}
	return nil
}
