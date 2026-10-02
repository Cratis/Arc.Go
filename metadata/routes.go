// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package metadata

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Endpoint is a resolved literal method/path pair. Resolve returns fresh values.
type Endpoint struct {
	// Identity is the artifact identity, unaffected by route options.
	Identity string
	// Method is POST, GET or QUERY.
	Method string
	// Path is the resolved path; explicit paths retain casing and trailing slashes.
	Path string
	// ValidateOnly identifies a command's validation endpoint.
	ValidateOnly bool
}

// CollisionError names both owners of a conflicting identity or method/path.
// Resolve sorts artifacts first so errors do not depend on registration order.
type CollisionError struct {
	// Key is the conflicting identity or HTTP method/path.
	Key string
	// First is the first owner in deterministic identity order.
	First string
	// Second is the second owner.
	Second string
}

// Error describes a collision without silently selecting a winning performer.
func (e *CollisionError) Error() string {
	return fmt.Sprintf("metadata collision %q between %q and %q", e.Key, e.First, e.Second)
}

type artifact struct {
	identity, namespace, name, path string
	command                         bool
}

// Resolve validates descriptor identities and returns routes sorted by identity and
// method. Conflicts are case-insensitive per HTTP method, including /validate.
// Commands and queries may share paths when methods differ. It never retains catalog.
// Only literal absolute paths are supported; templates and unsafe/ambiguous paths
// fail explicitly. An empty method override falls back to convention, not model path.
func Resolve(catalog Catalog, options Options) ([]Endpoint, error) {
	if catalog.Version != Version {
		return nil, fmt.Errorf("unsupported metadata version %d (want %d)", catalog.Version, Version)
	}
	if options.SegmentsToSkip < 0 {
		return nil, fmt.Errorf("segments to skip must not be negative")
	}
	artifacts := make([]artifact, 0, len(catalog.Commands)+len(catalog.Queries))
	for _, c := range catalog.Commands {
		artifacts = append(artifacts, artifact{c.Type.Identity(), c.Type.Namespace, c.Type.Name, c.Path, true})
	}
	for _, q := range catalog.Queries {
		if err := validateType(q.ReadModel); err != nil {
			return nil, err
		}
		path := q.ReadModelPath
		if q.Path != nil {
			path = *q.Path
		}
		artifacts = append(artifacts, artifact{q.Identity(), q.ReadModel.Namespace, q.Name, path, false})
	}
	slices.SortFunc(artifacts, func(a, b artifact) int { return strings.Compare(a.identity, b.identity) })
	groups := make(map[string]int)
	for i, a := range artifacts {
		if err := validateType(TypeName{Namespace: a.namespace, Name: a.name}); err != nil {
			return nil, err
		}
		if i > 0 && artifacts[i-1].identity == a.identity {
			return nil, &CollisionError{Key: a.identity, First: a.identity, Second: a.identity}
		}
		groups[group(a, options)]++
	}
	endpoints := make([]Endpoint, 0, len(artifacts)*2)
	seen := make(map[string]string)
	for _, a := range artifacts {
		include := options.IncludeQueryName
		if a.command {
			include = options.IncludeCommandName
		}
		path := a.path
		if path == "" {
			path = route(a, options, include || groups[group(a, options)] > 1)
		}
		if err := validatePath(path); err != nil {
			return nil, fmt.Errorf("route for %s: %w", a.identity, err)
		}
		routes := []Endpoint{{Identity: a.identity, Method: "GET", Path: path}}
		if a.command {
			routes = []Endpoint{{a.identity, "POST", path, false}, {a.identity, "POST", path + "/validate", true}}
		} else if options.EnableQueryHTTPMethod {
			routes = append(routes, Endpoint{Identity: a.identity, Method: "QUERY", Path: path})
		}
		for _, endpoint := range routes {
			key := endpoint.Method + " " + strings.ToLower(endpoint.Path)
			if first, ok := seen[key]; ok {
				return nil, &CollisionError{key, first, a.identity}
			}
			seen[key] = a.identity
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints, nil
}

func group(a artifact, o Options) string {
	return fmt.Sprintf("%t:%s", a.command, strings.Join(location(a.namespace, o.SegmentsToSkip), "."))
}

func route(a artifact, o Options, include bool) string {
	parts := []string{strings.Trim(o.RoutePrefix, "/")}
	for _, segment := range location(a.namespace, o.SegmentsToSkip) {
		parts = append(parts, kebab(segment))
	}
	if include {
		parts = append(parts, kebab(a.name))
	}
	path := "/" + strings.ToLower(strings.Join(parts, "/"))
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func kebab(value string) string {
	var b strings.Builder
	previous := rune(0)
	for i, c := range value {
		if c == '_' {
			b.WriteRune('-')
		} else {
			if i > 0 && unicode.IsUpper(c) && previous != '_' {
				b.WriteRune('-')
			}
			b.WriteRune(unicode.ToLower(c))
		}
		previous = c
	}
	return b.String()
}

func validateType(t TypeName) error {
	names := []string{t.Name}
	if t.Namespace != "" {
		names = append(names, strings.Split(t.Namespace, ".")...)
	}
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("empty identity segment in %q", t.Identity())
		}
		for i, c := range name {
			if c != '_' && !unicode.IsLetter(c) && (i == 0 || !unicode.IsDigit(c)) {
				return fmt.Errorf("invalid identity segment %q", name)
			}
		}
	}
	return nil
}

func validatePath(path string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must be absolute")
	}
	for _, c := range path {
		if unicode.IsSpace(c) || unicode.IsControl(c) || strings.ContainsRune("{}?#%\\", c) {
			return fmt.Errorf("path must be literal and unescaped")
		}
	}
	for _, s := range strings.Split(path, "/") {
		if s == "." || s == ".." {
			return fmt.Errorf("dot path segments are unsupported")
		}
	}
	if strings.Contains(path, "//") {
		return fmt.Errorf("repeated slashes are unsupported in explicit paths")
	}
	return nil
}
