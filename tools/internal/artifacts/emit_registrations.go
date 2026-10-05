// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"go/types"
	"strings"
)

// emitValidator emits the registration a caller would write by hand:
// validation.Register for a declared type and validation.RegisterScoped for a
// constructor, or their Concept variants.
func (e *emitter) emitValidator(v validator) {
	validation := e.imp(runtimePath + "/validation")
	model := e.typ(v.model)
	concept := ""
	if v.concept {
		concept = "Concept"
	}
	if v.constructor == nil {
		e.line("if %s := %s.Register%s[%s](%s.Validators(), %s); %s != nil { return %s }", e.err, validation, concept, model, e.builder, e.zeroValue(v.value), e.err, e.err)
		return
	}
	keys := e.manifest(v.constructor.call)
	e.line("if %s := %s.RegisterScoped%s[%s](%s.Validators(), func(%s %s.Context, %s *%s.Scope) (%s.Validator[%s], error) {", e.err, validation, concept, model, e.builder, e.ctx, e.imp("context"), e.scope, e.imp(runtimePath+"/execution"), validation, model)
	e.emitConstruction(v.constructor, "nil")
	e.line("}%s); %s != nil { return %s }", spread(keys), e.err, e.err)
}

// emitPolicy emits Policies().Register for a declared type and
// authorization.RegisterPolicy for a constructor.
func (e *emitter) emitPolicy(p policy) {
	authorization := e.imp(runtimePath + "/authorization")
	options := authorization + ".PolicyOptions{EvaluatesAnonymous:" + map[bool]string{true: "true", false: "false"}[p.anonymous] + "}"
	if p.constructor == nil {
		e.line("if %s := %s.Policies().Register(%q, %s, %s); %s != nil { return %s }", e.err, e.builder, p.name, e.zeroValue(p.value), options, e.err, e.err)
		return
	}
	keys := e.manifest(p.constructor.call)
	result := e.typ(p.constructor.result)
	e.line("if %s := %s.RegisterPolicy[%s](%s.Policies(), %q, func(%s %s.Context, %s *%s.Scope) (%s, error) {", e.err, authorization, result, e.builder, p.name, e.ctx, e.imp("context"), e.scope, e.imp(runtimePath+"/execution"), result)
	zero := e.unique("arcZero")
	e.emitConstruction(p.constructor, zero)
	e.line("}, %s%s); %s != nil { return %s }", options, spread(keys), e.err, e.err)
}

// emitConstruction resolves constructor dependencies stage-locally through
// ArcBindings and returns the constructed instance. failure is "nil" for
// interface results or the name of a zero variable declared on demand.
func (e *emitter) emitConstruction(c *constructor, failure string) {
	fail := func() string {
		if failure == "nil" {
			return "return nil, " + e.err
		}
		return "var " + failure + " " + e.typ(c.result) + "; return " + failure + ", " + e.err
	}
	args := make([]string, 0, len(c.call.params))
	for _, p := range c.call.params {
		if p.kind == "context" {
			args = append(args, e.ctx)
			continue
		}
		value := e.unique("arcDependency")
		e.line("%s, %s := %s.arc%s(%s, %s)", value, e.err, e.bindings, e.dep(p.typ).field, e.ctx, e.scope)
		e.line("if %s != nil { %s }", e.err, fail())
		args = append(args, value)
	}
	call := e.function(c.fn) + "(" + strings.Join(args, ", ") + ")"
	if !c.returnsError {
		e.line("return %s, nil", call)
		return
	}
	instance := e.unique("arcInstance")
	e.line("%s, %s := %s", instance, e.err, call)
	e.line("if %s != nil { %s }", e.err, fail())
	e.line("return %s, nil", instance)
}

func (e *emitter) function(fn *types.Func) string {
	if fn.Pkg() == e.analysis.pkg.Types {
		return fn.Name()
	}
	return e.imp(fn.Pkg().Path()) + "." + fn.Name()
}

// zeroValue renders the shared zero instance of a registered struct type.
func (e *emitter) zeroValue(t types.Type) string {
	if pointer, ok := t.(*types.Pointer); ok {
		return "&" + e.typ(pointer.Elem()) + "{}"
	}
	return e.typ(t) + "{}"
}

func spread(keys string) string {
	if keys == "" {
		return ""
	}
	return ", " + keys + "..."
}
