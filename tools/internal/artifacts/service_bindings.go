// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	bt "github.com/cratis/fundamentals.go/dependencyinjection/bindingtypes"
	"golang.org/x/tools/go/packages"
)

// Keep the shared plan intact; Existing is also needed when no generated key
// overlaps it. Catalog can attest presence, never a registration's lifetime.
type serviceBindingsPlan struct {
	owner    *analysis
	plan     bt.Plan
	existing []bt.Registration
}

func planServiceBindings(analyses []*analysis, cfg *serviceBindingsConfig, report io.Writer) (*serviceBindingsPlan, error) {
	if cfg == nil {
		return nil, nil
	}
	var owner *analysis
	refs := serviceReferences{packages: map[string]*types.Package{}}
	var universe []*types.Package
	for _, a := range analyses {
		if a.pkg.PkgPath == cfg.Package {
			owner = a
		}
		refs.packages[a.pkg.PkgPath] = a.pkg.Types
		universe = append(universe, a.pkg.Types)
		for _, imported := range a.pkg.Types.Imports() {
			refs.packages[imported.Path()] = imported
		}
	}
	if owner == nil {
		return nil, fmt.Errorf("bindings owner %q must be an explicitly selected main-module package", cfg.Package)
	}
	refs.owner = owner.pkg.Types
	for _, name := range []string{"ArcBindings", "RegisterArtifacts", "RegisterServices"} {
		if object := refs.owner.Scope().Lookup(name); object != nil {
			return nil, diagnostic(owner.pkg, object.Pos(), "generated symbol %s conflicts with a handwritten declaration", name)
		}
	}
	config := bt.Config{EmitPackage: refs.owner, MatchIFoo: cfg.MatchIFoo, RequireAllDependencies: cfg.RequireAllDependencies}
	if cfg.Duplicates == "keepExisting" {
		config.Duplicates = bt.KeepExisting
	}
	for _, a := range analyses {
		policies, diagnostics := bt.ReadDirectives(a.pkg.Syntax, a.pkg.TypesInfo)
		if err := reportBindingDiagnostics(analyses, diagnostics, report); err != nil {
			return nil, err
		}
		config.Policies = append(config.Policies, policies...)
	}
	if cfg.Constructors != nil {
		config.Constructors = make([]*types.Func, 0, len(cfg.Constructors))
		for _, reference := range cfg.Constructors {
			object, err := refs.object(reference)
			if err != nil {
				return nil, err
			}
			fn, ok := object.(*types.Func)
			if !ok {
				return nil, fmt.Errorf("constructor reference %q is not a function", reference)
			}
			config.Constructors = append(config.Constructors, fn)
		}
	}
	for _, pair := range cfg.Interfaces {
		service, err := refs.typ(pair.Service)
		if err != nil {
			return nil, err
		}
		implementation, err := refs.typ(pair.Implementation)
		if err != nil {
			return nil, err
		}
		config.Interfaces = append(config.Interfaces, bt.InterfaceBinding{Service: service, Implementation: implementation})
	}
	for _, existing := range cfg.Existing {
		key, err := refs.typ(existing.Key)
		if err != nil {
			return nil, err
		}
		lifetime, err := serviceLifetime(existing.Lifetime)
		if err != nil {
			return nil, err
		}
		config.Existing = append(config.Existing, bt.Registration{Service: key, Lifetime: lifetime})
	}
	plan := bt.Analyze(universe, config)
	if err := reportBindingDiagnostics(analyses, plan.Diagnostics, report); err != nil {
		return nil, err
	}
	for _, binding := range plan.Bindings {
		if binding.Constructor == nil {
			continue
		}
		for _, a := range analyses {
			for _, command := range a.commands {
				key := types.Unalias(binding.Service)
				if pointer, ok := key.(*types.Pointer); ok {
					key = types.Unalias(pointer.Elem())
				}
				if types.Identical(key, command.typ) {
					return nil, diagnostic(a.pkg, command.pos, "BT001 error: command DTO constructors are not service bindings")
				}
			}
		}
	}
	return &serviceBindingsPlan{owner: owner, plan: plan, existing: config.Existing}, nil
}

// validateServiceImports checks the renderer's exact service import set against
// the Go loader/compiler's package universe before any publication. Reuse the
// renderer so aliases, nested generic arguments, Existing keys and forwarding
// keys cannot diverge from this check. The import-only analysis overlay lets Go
// enforce main/internal accessibility and direct/transitive cycles itself; it
// neither executes application code nor adds services to the wire type graph.
func validateServiceImports(load *packages.Config, patterns []string, plan *serviceBindingsPlan) error {
	if plan == nil {
		return nil
	}
	e := &emitter{analysis: plan.owner, imports: map[string]string{}, names: map[string]bool{}}
	e.emitServices(plan)
	paths := make([]string, 0, len(e.imports))
	for path := range e.imports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var source strings.Builder
	fmt.Fprintf(&source, "package %s\n", plan.owner.pkg.Name)
	for _, path := range paths {
		fmt.Fprintf(&source, "import _ %s\n", strconv.Quote(path))
	}
	dir, err := packageDirectory(plan.owner.pkg)
	if err != nil {
		return err
	}
	check := *load
	check.Overlay = make(map[string][]byte, len(load.Overlay)+1)
	for path, content := range load.Overlay {
		check.Overlay[path] = content
	}
	check.Overlay[filepath.Join(dir, Filename)] = []byte(source.String())
	check.Mode = packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedTypesSizes
	loaded, err := packages.Load(&check, patterns...)
	if err != nil {
		return fmt.Errorf("validate generated service imports: %w", err)
	}
	if len(loaded) == 0 {
		return fmt.Errorf("validate generated service imports: no packages matched")
	}
	messages := map[string]bool{}
	packages.Visit(loaded, func(pkg *packages.Package) bool {
		for _, err := range pkg.Errors {
			messages[err.Error()] = true
		}
		return true
	}, nil)
	if len(messages) > 0 {
		lines := make([]string, 0, len(messages))
		for message := range messages {
			lines = append(lines, message)
		}
		sort.Strings(lines)
		return fmt.Errorf("validate generated service imports: %s", strings.Join(lines, "\n"))
	}
	return nil
}

func reportBindingDiagnostics(analyses []*analysis, diagnostics []bt.Diagnostic, report io.Writer) error {
	var failures []string
	for _, d := range diagnostics {
		location := "bindings configuration"
		if d.Pos.IsValid() {
			for _, a := range analyses {
				if file := a.pkg.Fset.File(d.Pos); file != nil {
					location = a.pkg.Fset.Position(d.Pos).String()
					break
				}
			}
		}
		line := fmt.Sprintf("%s: %s %s: %s", location, d.Code, d.Severity, d.Message)
		if d.Type != nil {
			line += " [" + types.TypeString(d.Type, func(p *types.Package) string { return p.Path() }) + "]"
		}
		if d.Severity == bt.Error {
			failures = append(failures, line)
		} else if report != nil {
			if _, err := fmt.Fprintln(report, line); err != nil {
				return err
			}
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("%s", strings.Join(failures, "\n"))
	}
	return nil
}

func reportServices(report io.Writer, plan *serviceBindingsPlan, check bool) error {
	if report == nil || plan == nil {
		return nil
	}
	registrations, retained := 0, 0
	for _, binding := range plan.plan.Bindings {
		if binding.Action == bt.RetainExisting {
			retained++
		} else {
			registrations++
		}
	}
	verb := "published"
	if check {
		verb = "verified"
	}
	_, err := fmt.Fprintf(report, "arc-gen: %d service bindings %s, %d explicitly retained (%s)\n", registrations, verb, retained, plan.owner.pkg.PkgPath)
	return err
}
