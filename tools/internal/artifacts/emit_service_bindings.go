// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"strings"

	di "github.com/cratis/fundamentals.go/dependencyinjection"
	bt "github.com/cratis/fundamentals.go/dependencyinjection/bindingtypes"
)

const serviceDIPath = "github.com/cratis/fundamentals.go/dependencyinjection"

func (e *emitter) emitServices(plan *serviceBindingsPlan) {
	if plan == nil {
		return
	}
	registrar := e.unique("arcRegistrar")
	catalog := e.unique("arcCatalog")
	resolver := e.unique("arcResolver")
	diName := e.imp(serviceDIPath)
	e.line("// RegisterServices registers deferred constructor services separately from artifacts.")
	e.line("// Existing lifetimes are caller attestations; Catalog, when available, checks presence only.")
	e.line("// No factory runs here. Errors propagate unchanged; discard partial composition after an error.")
	e.line("func RegisterServices(%s %s.Registrar) error {", registrar, diName)
	e.line("if %s == nil { return %s.ErrInvalidRegistration }", registrar, diName)
	if len(plan.existing) > 0 {
		e.line("if %s, ok := %s.(%s.Catalog); ok {", catalog, registrar, diName)
		for _, existing := range plan.existing {
			e.line("if !%s.Contains(%s.KeyFor[%s]()) { return %s.Errorf(\"arc-gen: Existing key %%s is absent: %%w\", %s.KeyFor[%s](), %s.ErrMissing) }", catalog, diName, e.typ(existing.Service), e.imp("fmt"), diName, e.typ(existing.Service), diName)
		}
		e.line("}")
	}
	for _, binding := range plan.plan.Bindings {
		if binding.Action == bt.RetainExisting {
			continue
		}
		lifetime := map[di.Lifetime]string{di.Singleton: "Singleton", di.Scoped: "Scoped", di.Transient: "Transient"}[binding.Lifetime]
		if binding.Forward != nil {
			e.line("if %s := %s.BindBorrowed(%s, %s.%s, func(%s %s.Context, %s %s.Resolver) (%s, error) {", e.err, diName, registrar, diName, lifetime, e.ctx, e.imp("context"), resolver, diName, e.typ(binding.Service))
			e.line("return %s.Resolve[%s](%s, %s)", diName, e.typ(binding.Forward), e.ctx, resolver)
			e.line("}, %s.KeyFor[%s]()); %s != nil { return %s }", diName, e.typ(binding.Forward), e.err, e.err)
			continue
		}
		function := binding.Constructor.Name()
		if pkg := binding.Constructor.Pkg(); pkg != e.analysis.pkg.Types {
			function = e.imp(pkg.Path()) + "." + function
		}
		arguments := make([]string, len(binding.Arguments))
		for i := range arguments {
			arguments[i] = e.unique("arcDependency")
		}
		arity := len(arguments)
		if arity >= 1 && arity <= 4 {
			adapter := []string{"", "BindFunc1", "BindFunc2", "BindFunc3", "BindFunc4"}[arity]
			e.line("if %s := %s.%s(%s, %s.%s, func(%s %s.Context,", e.err, diName, adapter, registrar, diName, lifetime, e.ctx, e.imp("context"))
			for i, argument := range arguments {
				e.line("%s %s,", argument, e.typ(binding.Arguments[i]))
			}
			e.line(") (%s, error) {", e.typ(binding.Service))
		} else {
			e.line("if %s := %s.Bind(%s, %s.%s, func(%s %s.Context, %s %s.Resolver) (%s, error) {", e.err, diName, registrar, diName, lifetime, e.ctx, e.imp("context"), resolver, diName, e.typ(binding.Service))
			for i, argument := range arguments {
				e.line("%s, %s := %s.Resolve[%s](%s, %s)", argument, e.err, diName, e.typ(binding.Arguments[i]), e.ctx, resolver)
				e.line("if %s != nil { var %s %s; return %s, %s }", e.err, e.zero, e.typ(binding.Service), e.zero, e.err)
			}
		}
		call := arguments
		if binding.PassContext {
			call = append([]string{e.ctx}, arguments...)
		}
		suffix := ", nil"
		if binding.ReturnsError {
			suffix = ""
		}
		e.line("return %s(%s)%s", function, strings.Join(call, ", "), suffix)
		var keys []string
		if arity > 4 {
			for _, key := range binding.Dependencies {
				keys = append(keys, diName+".KeyFor["+e.typ(key)+"]()")
			}
		}
		tail := ""
		if len(keys) > 0 {
			tail = ", " + strings.Join(keys, ", ")
		}
		e.line("}%s); %s != nil { return %s }", tail, e.err, e.err)
	}
	e.line("return nil\n}")
}
