// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package metadata defines versioned, explicitly authored Arc artifact descriptors.
// Runtime adapters and future generators share these identities and route decisions;
// no reflection discovery, invocation or network activity occurs here.
package metadata

import "strings"

// Version is the supported descriptor format. Readers reject other versions.
const Version = 1

// TypeName is a stable logical type identity, independent of Go package relocation.
// Namespace uses dot-separated C#-compatible segments; Name is a simple type name.
// Both are supplied explicitly rather than inferred from runtime Go type names.
type TypeName struct {
	// Namespace is the logical namespace; empty denotes the global namespace.
	Namespace string `json:"namespace"`
	// Name is the simple model or command name.
	Name string `json:"name"`
}

// Identity returns the namespace-qualified type name without route normalization.
func (t TypeName) Identity() string {
	if t.Namespace == "" {
		return t.Name
	}
	return t.Namespace + "." + t.Name
}

// Command describes a model-bound command, not its executable handler.
type Command struct {
	// Type identifies the command and supplies its conventional route location.
	Type TypeName `json:"type"`
	// Path overrides the entire route verbatim; empty uses convention.
	Path string `json:"path,omitempty"`
	// Authorization is the optional command declaration; nil uses fallback.
	Authorization *Authorization `json:"authorization,omitempty"`
}

// Query describes a method on a read model. Its identity includes the model name,
// but its conventional route includes only the namespace and method name.
type Query struct {
	// ReadModel identifies the containing read model.
	ReadModel TypeName `json:"readModel"`
	// Name is the method name, not the read model name.
	Name string `json:"name"`
	// Path is the method override. A non-nil empty value disables the model override.
	Path *string `json:"path,omitempty"`
	// ReadModelPath is the model-level route override.
	ReadModelPath string `json:"readModelPath,omitempty"`
	// Observable indicates an observable return shape; it does not start a stream.
	Observable bool `json:"observable"`
	// Authorization replaces the read-model declaration when nonnil.
	Authorization *Authorization `json:"authorization,omitempty"`
	// ReadModelAuthorization applies only when no method declaration exists.
	ReadModelAuthorization *Authorization `json:"readModelAuthorization,omitempty"`
}

// Identity returns the stable fully qualified query name used by subscriptions.
func (q Query) Identity() string { return q.ReadModel.Identity() + "." + q.Name }

// Catalog is descriptor format v1 for endpoint identity and route metadata. It is
// caller-owned mutable configuration. Resolve borrows it only during the call.
// Authorization compilation is separate from route resolution. Use keyed literals
// for public descriptors as optional fields may be added during v0 development.
type Catalog struct {
	// Version must equal metadata.Version; zero is not silently upgraded.
	Version int `json:"version"`
	// Commands is the complete command set for namespace conflict resolution.
	Commands []Command `json:"commands"`
	// Queries is the complete query set for namespace conflict resolution.
	Queries []Query `json:"queries"`
}

// Options mirrors ApiEndpointOptions. Use DefaultOptions for C# defaults; zero
// deliberately means an empty prefix, omitted names and disabled QUERY support.
type Options struct {
	// RoutePrefix is trimmed of leading/trailing slashes before conventional routing.
	RoutePrefix string
	// SegmentsToSkip removes leading namespace segments; negative values are invalid.
	SegmentsToSkip int
	// IncludeCommandName adds the kebab-cased command name even without conflicts.
	IncludeCommandName bool
	// IncludeQueryName adds the kebab-cased query method name even without conflicts.
	IncludeQueryName bool
	// EnableQueryHTTPMethod adds QUERY beside GET.
	EnableQueryHTTPMethod bool
}

// DefaultOptions returns independent C# defaults: api, no skips, names and QUERY.
func DefaultOptions() Options {
	return Options{RoutePrefix: "api", IncludeCommandName: true, IncludeQueryName: true, EnableQueryHTTPMethod: true}
}

func location(namespace string, skip int) []string {
	if namespace == "" {
		return nil
	}
	segments := strings.Split(namespace, ".")
	if skip >= len(segments) {
		return nil
	}
	return segments[skip:]
}
