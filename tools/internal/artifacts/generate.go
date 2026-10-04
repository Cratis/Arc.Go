// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Config selects the package boundary. Dir defaults to the current directory;
// Patterns defaults to ".". Tags is the standard comma-separated build-tag list.
// Check verifies output without writing. Only the selected build configuration
// is generated; use separate packages for incompatible artifact sets.
type Config struct {
	Dir        string
	Patterns   []string
	Tags       string
	Check      bool
	ConfigFile string
	// BindingsConfigFile opts into a separate versioned constructor-service plan.
	BindingsConfigFile string
	Profile            *ApplicationProfile
	TypeScriptOut      string
	EmitGo             *bool
	// Report receives the successful supported-family inventory, if nonnil.
	Report io.Writer
}

// Generate type-checks selected main-module packages without executing user code.
// It validates all selected packages before changing output. Writes are atomic
// per file, not across packages; any write failure is returned to the caller.
// Existing owned output is overlaid during analysis, so stale adapters cannot
// prevent regeneration. Handwritten files and dependency modules are never edited.
func Generate(ctx context.Context, config Config) error {
	var servicesConfig *serviceBindingsConfig
	if config.BindingsConfigFile != "" {
		loaded, err := readServiceBindingsConfig(config.BindingsConfigFile)
		if err != nil {
			return err
		}
		servicesConfig = loaded
	}
	profile := ApplicationProfile{FormatVersion: GraphVersion, Name: "adapter-only"}
	if config.ConfigFile != "" {
		loaded, err := readProfile(config.ConfigFile)
		if err != nil {
			return err
		}
		profile = loaded
	}
	if config.Profile != nil {
		profile = *config.Profile
		if err := validateProfile(profile); err != nil {
			return err
		}
	}
	if config.TypeScriptOut != "" {
		profile.TypeScript.Out = config.TypeScriptOut
	}
	if config.EmitGo != nil {
		profile.TypeScript.EmitGo = config.EmitGo
	}
	if err := validateProfile(profile); err != nil {
		return err
	}
	if profile.OpenAPI != nil {
		return fmt.Errorf("OpenAPI profile contract analysis is internal only; document generation/publication is not enabled")
	}
	typescript := profile.TypeScript.Out != ""
	if !typescript && profile.TypeScript.EmitGo != nil && !*profile.TypeScript.EmitGo {
		return fmt.Errorf("emit-go=false requires TypeScript output")
	}
	if typescript && profile.TypeScript.EmitGo != nil && !*profile.TypeScript.EmitGo {
		return fmt.Errorf("proxy-only publication requires explicit runtime endpoint verification; emit-go=false is not supported")
	}
	patterns := config.Patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	load := &packages.Config{Context: ctx, Dir: config.Dir, Mode: packages.NeedName | packages.NeedFiles | packages.NeedModule, Overlay: map[string][]byte{}}
	if config.Tags != "" {
		load.BuildFlags = []string{"-tags=" + config.Tags}
	}
	listed, err := packages.Load(load, patterns...)
	if err != nil {
		return fmt.Errorf("list selected packages: %w", err)
	}
	if len(listed) == 0 {
		return fmt.Errorf("no packages matched")
	}
	for _, pkg := range listed {
		if pkg.Module == nil || !pkg.Module.Main {
			return fmt.Errorf("%s: select packages in the current module, not dependencies", pkg.PkgPath)
		}
		dir, err := packageDirectory(pkg)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, Filename)
		content, exists, err := readOutput(path)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if !owned(content) {
			return fmt.Errorf("%s: refusing to overwrite unowned output", path)
		}
		// Find the handwritten package clause even when the old generated file
		// contains syntax errors, missing imports, or a stale package name.
		name, err := sourcePackageName(pkg, path)
		if err != nil {
			return err
		}
		load.Overlay[path] = []byte("package " + name + "\n")
	}
	load.Mode = packages.NeedName | packages.NeedFiles | packages.NeedModule | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedTypesSizes
	if typescript {
		load.Mode |= packages.NeedDeps
	}
	loaded, err := packages.Load(load, patterns...)
	if err != nil {
		return fmt.Errorf("load selected packages: %w", err)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].PkgPath < loaded[j].PkgPath })
	type output struct {
		path    string
		content []byte
	}
	var outputs []output
	var analyses []*analysis
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			messages := make([]string, 0, len(pkg.Errors))
			for _, e := range pkg.Errors {
				messages = append(messages, e.Error())
			}
			sort.Strings(messages)
			return fmt.Errorf("%s", strings.Join(messages, "\n"))
		}
		a, err := analyze(pkg)
		if err != nil {
			return err
		}
		analyses = append(analyses, a)
	}
	services, err := planServiceBindings(analyses, servicesConfig, config.Report)
	if err != nil {
		return err
	}
	graph, err := buildGraph(analyses, profile, typescript)
	if err != nil {
		return err
	}
	var proxies []typescriptOutput
	if typescript {
		proxies, err = renderTypeScriptQueries(graph)
		if err != nil {
			return err
		}
	}
	for _, a := range analyses {
		dir, err := packageDirectory(a.pkg)
		if err != nil {
			return err
		}
		var data []byte
		var packageServices *serviceBindingsPlan
		if services != nil && services.owner == a {
			packageServices = services
		}
		if len(a.commands)+len(a.models) > 0 || hasDerivedModels(a) || packageServices != nil {
			data, err = emit(a, packageServices)
			if err != nil {
				return err
			}
		}
		outputs = append(outputs, output{filepath.Join(dir, Filename), data})
	}
	if typescript {
		plan := make([]ownedOutput, 0, len(outputs)+len(proxies))
		for _, out := range outputs {
			plan = append(plan, ownedOutput{Path: out.path, Content: out.content})
		}
		outRoot := profile.TypeScript.Out
		if !filepath.IsAbs(outRoot) {
			outRoot = filepath.Join(loaded[0].Module.Dir, outRoot)
		}
		for _, out := range proxies {
			plan = append(plan, ownedOutput{Path: filepath.Join(outRoot, filepath.FromSlash(out.path)), Content: out.content})
		}
		if err := publishOwned(ctx, loaded[0].Module.Dir, outRoot, profile, graph, config.Tags, plan, config.Check, nil); err != nil {
			return err
		}
		if config.Report != nil {
			adapters := 0
			for _, out := range outputs {
				if out.content != nil {
					adapters++
				}
			}
			_, err := fmt.Fprintf(config.Report, "arc-gen: %d Go adapters and %d TypeScript model/command/query/barrel files %s (profile %s, fingerprint %s)\n", adapters, len(proxies), map[bool]string{true: "verified", false: "published"}[config.Check], profile.Name, graph.Fingerprint)
			if err != nil {
				return err
			}
		}
		return reportServices(config.Report, services, config.Check)
	}
	// Preflight every output before the first write, including newly appeared files.
	for _, out := range outputs {
		previous, exists, err := readOutput(out.path)
		if err != nil {
			return err
		}
		if exists && !owned(previous) {
			return fmt.Errorf("%s: refusing to overwrite unowned output", out.path)
		}
		if config.Check && !bytes.Equal(previous, out.content) {
			return fmt.Errorf("%s: generated adapters are stale; run arc-gen", out.path)
		}
	}
	if config.Check {
		return reportServices(config.Report, services, true)
	}
	for _, out := range outputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		previous, exists, err := readOutput(out.path)
		if err != nil {
			return err
		}
		if exists && !owned(previous) {
			return fmt.Errorf("%s: refusing to overwrite unowned output", out.path)
		}
		if bytes.Equal(previous, out.content) {
			continue
		}
		if out.content == nil {
			if exists {
				if err := os.Remove(out.path); err != nil {
					return fmt.Errorf("remove stale adapter: %w", err)
				}
			}
		} else if err := writeOutput(out.path, out.content); err != nil {
			return err
		}
	}
	return reportServices(config.Report, services, false)
}

func packageDirectory(pkg *packages.Package) (string, error) {
	if len(pkg.GoFiles) > 0 {
		return filepath.Dir(pkg.GoFiles[0]), nil
	}
	if len(pkg.Errors) > 0 {
		return "", fmt.Errorf("%s", pkg.Errors[0])
	}
	return "", fmt.Errorf("%s: no active Go files", pkg.PkgPath)
}
func sourcePackageName(pkg *packages.Package, generated string) (string, error) {
	for _, path := range pkg.GoFiles {
		if path == generated {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if err != nil {
			return "", err
		}
		return file.Name.Name, nil
	}
	return pkg.Name, nil
}
func owned(data []byte) bool {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	return bytes.HasPrefix(line, []byte("// Code generated by arc-gen ")) && bytes.HasSuffix(line, []byte("; DO NOT EDIT."))
}
func readOutput(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s: generated output must be a regular file", path)
	}
	data, err := os.ReadFile(path)
	return data, true, err
}
func writeOutput(path string, content []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".arc-gen-*")
	if err != nil {
		return err
	}
	defer func() {
		removeErr := os.Remove(file.Name())
		if !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if _, err = file.Write(content); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Chmod(0644); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("publish generated adapter: %w", err)
	}
	return nil
}
