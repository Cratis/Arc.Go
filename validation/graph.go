// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/internal/modelshape"
	fconcepts "github.com/cratis/fundamentals.go/concepts"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Graph holds immutable registrations and is concurrent-safe when its validators
// are. Zero (and nil) enables model methods and tags without registered rules.
// Maps are opaque: register an explicit validator to inspect their entries.
type Graph struct {
	entries map[reflect.Type]graphRegistration
	order   []reflect.Type
}

// CheckDependencies validates every registered scoped validator's declared keys
// against a borrowed catalog, without resolving dependencies or running rules.
// All registrations are checked because runtime interfaces may expose any model.
// Nil/zero graphs and graphs without keys need no catalog. A missing or typed-nil
// catalog fails when keys exist. Hidden closure dependencies cannot be checked.
func (g *Graph) CheckDependencies(catalog di.Catalog) error {
	if g == nil {
		return nil
	}
	for _, typ := range g.order {
		for _, key := range g.entries[typ].keys {
			if isNil(catalog) || !catalog.Contains(key) {
				return fmt.Errorf("%w: %s requires %s", ErrInvalidRegistration, typ, key)
			}
		}
	}
	return nil
}

type visit struct {
	typ     reflect.Type
	pointer uintptr
}
type walker struct {
	graph   *Graph
	ctx     context.Context
	scope   *execution.Scope
	visited map[visit]bool
	results []Result
	errors  []error
}

// Validate runs each nonnull node's exact registry validator, then its model
// method, before descending readable fields/collection elements. Pointer identity
// stops cycles/shared instances; equal distinct instances still run. Collections
// keep the owning path without indices. Concept registrations are opaque leaves.
// Root required tags run separately after the graph. Results/error semantics
// match Invoke: Failure findings remain on the returned error, not duplicated in
// results. Factory failures are infrastructure errors, never validator failures.
// Scope is optional for direct validators; scoped factories require a valid scope.
func (g *Graph) Validate(ctx context.Context, scope *execution.Scope, model any) (results []Result, err error) {
	if ctx == nil {
		return nil, ErrInvalidValidator
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g == nil {
		g = &Graph{}
	}
	run := func(ctx context.Context, view *execution.Scope) error {
		w := walker{graph: g, ctx: ctx, scope: view, visited: make(map[visit]bool)}
		if walkErr := w.walk(reflect.ValueOf(model), "", false, 0); walkErr != nil {
			return errors.Join(append(w.errors, walkErr)...)
		}
		tags, tagErr := ValidateTags(model)
		results = append(w.results, tags...)
		return errors.Join(append(w.errors, tagErr)...)
	}
	if scope != nil {
		err = scope.Use(ctx, run)
	} else {
		err = run(ctx, nil)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return results, err
}

func (w *walker) walk(v reflect.Value, path string, skipConcept bool, depth int) error {
	if err := w.check(); err != nil {
		return err
	}
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return w.walk(v.Elem(), path, skipConcept, depth)
	}
	if isNil(v.Interface()) {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		key := visit{v.Type(), v.Pointer()}
		if w.visited[key] {
			return nil
		}
		w.visited[key] = true
	}
	if depth >= modelshape.MaxDepth {
		return ErrGraphDepth
	}
	registration, registered := w.graph.entries[v.Type()]
	concept, err := w.graph.isConcept(v.Type())
	if err != nil {
		return err
	}
	if registered && (!concept || !skipConcept) {
		results, err := registration.invoke(w.ctx, w.scope, v.Interface())
		if err := w.collect(results, err, path, concept); err != nil {
			return err
		}
	}
	if model, ok := v.Interface().(ModelValidator); ok {
		results, err := Invoke(w.ctx, ValidatorFunc[ModelValidator](func(ctx context.Context, m ModelValidator) ([]Result, error) { return m.Validate(ctx) }), model)
		if err := w.collect(results, err, path, concept); err != nil {
			return err
		}
	}
	if concept {
		return nil
	}
	// Dereference the representation without re-running value methods/validators:
	// pointer and value registrations are distinct runtime nodes, not fallbacks.
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
		if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			return w.walk(v, path, false, depth+1)
		}
	}
	switch v.Kind() {
	case reflect.Struct:
		fields, err := modelshape.Children(v.Type())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
		}
		for _, field := range fields {
			tags, err := ParseTags(field.Tag.Get("validate"))
			if err != nil {
				return err
			}
			child := modelshape.Value(v, field.Index)
			if child.IsValid() && !child.CanInterface() {
				continue
			}
			if err := w.walk(child, member(path, field.Name), tags.SkipConcept, depth+1); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if err := w.walk(v.Index(i), path, false, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) check() error {
	if w.scope != nil {
		return w.scope.CheckContext(w.ctx)
	}
	return w.ctx.Err()
}

// collect treats joined branches separately and keeps InvocationError terminal.
func (w *walker) collect(results []Result, err error, path string, concept bool) error {
	if check := w.check(); check != nil {
		return errors.Join(err, check)
	}
	w.results = append(w.results, atPath(results, path, concept)...)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		if _, terminal := err.(*InvocationError); !terminal {
			for _, branch := range joined.Unwrap() {
				if stop := w.collect(nil, branch, path, concept); stop != nil {
					return stop
				}
			}
			return nil
		}
	}
	if failure, ok := err.(Failure); ok {
		w.errors = append(w.errors, &graphFailure{cause: err, results: atPath(failure.ValidationResults(), path, concept)})
		return nil
	}
	if cause := errors.Unwrap(err); cause != nil {
		before := len(w.errors)
		if stop := w.collect(nil, cause, path, concept); stop != nil {
			if len(w.errors) != before {
				return stop
			}
			return err
		}
		// Keep outer diagnostic identities while using the mapped child findings.
		if len(w.errors) == before+1 {
			if failure, ok := w.errors[before].(Failure); ok {
				w.errors[before] = &graphFailure{cause: err, results: failure.ValidationResults()}
			}
		}
		return nil
	}
	return err
}

type graphFailure struct {
	cause   error
	results []Result
}

func (e *graphFailure) Error() string               { return "model validation failed" }
func (e *graphFailure) Unwrap() error               { return e.cause }
func (e *graphFailure) ValidationResults() []Result { return cloneResults(e.results) }

func atPath(results []Result, path string, concept bool) []Result {
	results = cloneResults(results)
	for i := range results {
		if concept && path != "" {
			results[i].Members = []string{path}
			continue
		}
		for j, name := range results[i].Members {
			if concept && strings.EqualFold(name, "value") {
				name = ""
			}
			results[i].Members[j] = member(path, name)
		}
	}
	return results
}
func member(path, name string) string {
	if path == "" {
		return name
	}
	if name == "" {
		return path
	}
	return path + "." + name
}

// CheckType validates statically visible graph/tag shapes without invoking
// validators. Pipeline Build can use it for input types without registry rules.
// Interface-held runtime types remain subject to validation-time checks.
func (g *Graph) CheckType(t reflect.Type) error {
	if t == nil {
		return ErrInvalidRegistration
	}
	if g == nil {
		g = &Graph{}
	}
	return g.checkShape(t, make(map[reflect.Type]bool), 0)
}

func (g *Graph) isConcept(t reflect.Type) (bool, error) {
	if entry, ok := g.entries[t]; ok && entry.concept {
		return true, nil
	}
	_, recognized, err := fconcepts.Underlying(t)
	return recognized, err
}

func (g *Graph) checkShape(t reflect.Type, visited map[reflect.Type]bool, depth int) error {
	if visited[t] {
		return nil
	}
	if depth >= modelshape.MaxDepth {
		return ErrGraphDepth
	}
	visited[t] = true
	if concept, err := g.isConcept(t); err != nil || concept {
		return err
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Array, reflect.Slice:
		return g.checkShape(t.Elem(), visited, depth+1)
	case reflect.Struct:
		fields, err := modelshape.Children(t)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
		}
		for _, field := range fields {
			if _, err := ParseTags(field.Tag.Get("validate")); err != nil {
				return err
			}
			if err := g.checkShape(field.Type, visited, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
