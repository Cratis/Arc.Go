// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cratis/arc.go/metadata"
)

// ApplicationProfile is versioned static application configuration. Nil routing
// retains metadata.DefaultOptions; omitted booleans retain documented defaults.
// A namespace may be explicitly global (the empty string).
type ApplicationProfile struct {
	FormatVersion     int                      `json:"formatVersion"`
	Name              string                   `json:"name"`
	DefaultNamespace  *string                  `json:"defaultNamespace,omitempty"`
	PackageNamespaces map[string]string        `json:"packageNamespaces,omitempty"`
	Routes            *RouteProfile            `json:"routes,omitempty"`
	TypeScript        TypeScriptProfile        `json:"typescript"`
	Responses         map[string]string        `json:"responses,omitempty"`
	ClientHTTP        map[string]string        `json:"clientHttp,omitempty"`
	TypeRoots         []string                 `json:"typeRoots,omitempty"`
	Imports           map[string]ImportMapping `json:"imports,omitempty"`
}

// RouteProfile overrides only explicitly supplied route values.
type RouteProfile struct {
	RoutePrefix           *string `json:"routePrefix,omitempty"`
	SegmentsToSkip        *int    `json:"segmentsToSkip,omitempty"`
	IncludeCommandName    *bool   `json:"includeCommandName,omitempty"`
	IncludeQueryName      *bool   `json:"includeQueryName,omitempty"`
	EnableQueryHTTPMethod *bool   `json:"enableQueryHttpMethod,omitempty"`
}

// TypeScriptProfile controls files only, never deployment base paths.
type TypeScriptProfile struct {
	Out               string          `json:"out,omitempty"`
	EmitGo            *bool           `json:"emitGo,omitempty"`
	SegmentsToSkip    *int            `json:"segmentsToSkip,omitempty"`
	NamespaceRoots    []NamespaceRoot `json:"namespaceRoots,omitempty"`
	ProxyFileSuffix   bool            `json:"proxyFileSuffix,omitempty"`
	SourceGrouping    bool            `json:"sourceGrouping,omitempty"`
	Interfaces        bool            `json:"interfaces,omitempty"`
	Library           bool            `json:"library,omitempty"`
	ExcludeTypes      []string        `json:"excludeTypes,omitempty"`
	ExcludeNamespaces []string        `json:"excludeNamespaces,omitempty"`
	ClientVersion     string          `json:"clientVersion,omitempty"`
}

// NamespaceRoot maps the longest exact/dot-prefix namespace to a safe folder.
type NamespaceRoot struct {
	Namespace string `json:"namespace"`
	Folder    string `json:"folder"`
}

// ImportMapping declares an opaque application's wire type and runtime constructor.
// It is not concept recognition; concept methods remain diagnostic until #15.
type ImportMapping struct {
	Type        string `json:"type"`
	Constructor string `json:"constructor"`
	Module      string `json:"module"`
}

func readProfile(path string) (ApplicationProfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return ApplicationProfile{}, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var profile ApplicationProfile
	if err := decoder.Decode(&profile); err != nil {
		return profile, fmt.Errorf("generator configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return profile, fmt.Errorf("trailing generator configuration")
	}
	return profile, validateProfile(profile)
}

func validateProfile(profile ApplicationProfile) error {
	if profile.FormatVersion != GraphVersion {
		return fmt.Errorf("unsupported generator configuration format %d", profile.FormatVersion)
	}
	if profile.Name == "" {
		return fmt.Errorf("generator profile requires a stable name")
	}
	if profile.TypeScript.ClientVersion != "" && profile.TypeScript.ClientVersion != "22.48.2" {
		return fmt.Errorf("unsupported frontend compatibility profile %q", profile.TypeScript.ClientVersion)
	}
	if profile.routeOptions().SegmentsToSkip < 0 || profile.TypeScript.SegmentsToSkip != nil && *profile.TypeScript.SegmentsToSkip < 0 {
		return fmt.Errorf("namespace segment stripping must not be negative")
	}
	seen := map[string]bool{}
	for _, root := range profile.TypeScript.NamespaceRoots {
		if root.Namespace == "" || seen[root.Namespace] || !safeRelative(root.Folder) {
			return fmt.Errorf("duplicate or unsafe namespace root %q", root.Namespace)
		}
		seen[root.Namespace] = true
	}
	for _, mapping := range profile.Imports {
		if mapping.Type == "" || mapping.Constructor == "" || mapping.Module == "" || strings.ContainsAny(mapping.Module, "\n\r'\"\\") {
			return fmt.Errorf("incomplete or unsafe import mapping")
		}
	}
	return nil
}

func safeRelative(path string) bool {
	if path == "" {
		return true
	}
	if filepath.IsAbs(path) || strings.ContainsAny(path, "\\:\x00\n\r<>|?*") {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
		if segment == ".." || segment == "." || segment == "" || strings.TrimRight(segment, " .") != segment || reserved {
			return false
		}
	}
	return true
}

func (profile ApplicationProfile) routeOptions() metadata.Options {
	options := metadata.DefaultOptions()
	if profile.Routes == nil {
		return options
	}
	route := profile.Routes
	if route.RoutePrefix != nil {
		options.RoutePrefix = *route.RoutePrefix
	}
	if route.SegmentsToSkip != nil {
		options.SegmentsToSkip = *route.SegmentsToSkip
	}
	if route.IncludeCommandName != nil {
		options.IncludeCommandName = *route.IncludeCommandName
	}
	if route.IncludeQueryName != nil {
		options.IncludeQueryName = *route.IncludeQueryName
	}
	if route.EnableQueryHTTPMethod != nil {
		options.EnableQueryHTTPMethod = *route.EnableQueryHTTPMethod
	}
	return options
}
