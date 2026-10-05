// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

// GraphVersion versions the richer contract projection separately from metadata.
const GraphVersion = 1

// ContractGraphVersion adds directional wire contracts and application assertions.
// The v1 projection remains stable for existing adapter and TypeScript profiles.
const ContractGraphVersion = 2

// Graph is the shared normalized Go/TypeScript/future OpenAPI contract. Compiler attachments stay in
// analysis; this projection can be serialized without AST or go/types objects.
type Graph struct {
	FormatVersion   int `json:"formatVersion"`
	verifyEndpoints bool
	Profile         ApplicationProfile  `json:"profile"`
	Catalog         metadata.Catalog    `json:"catalog"`
	Endpoints       []metadata.Endpoint `json:"endpoints"`
	Packages        []PackageDescriptor `json:"packages"`
	Types           []TypeDescriptor    `json:"types"`
	Commands        []CommandDescriptor `json:"commands"`
	Queries         []QueryDescriptor   `json:"queries"`
	Diagnostics     []string            `json:"diagnostics,omitempty"`
	Fingerprint     string              `json:"fingerprint"`
	Assertions      *ProfileAssertions  `json:"assertions,omitempty"`
	Framework       []FrameworkContract `json:"framework,omitempty"`
}

type PackageDescriptor struct {
	GoPath    string   `json:"goPath"`
	Namespace string   `json:"namespace"`
	Sources   []string `json:"sources"`
}

// WireType separates optionality, cardinality and exact runtime constructors.
type WireType struct {
	Kind     string        `json:"kind"`
	Target   string        `json:"target,omitempty"`
	Element  *WireType     `json:"element,omitempty"`
	Nullable bool          `json:"nullable,omitempty"`
	Contract *WireContract `json:"contract,omitempty"`
}

type FieldDescriptor struct {
	Name       string                      `json:"name"`
	Type       WireType                    `json:"type"`
	Optional   bool                        `json:"optional"`
	OmitEmpty  bool                        `json:"omitEmpty,omitempty"`
	OmitZero   bool                        `json:"omitZero,omitempty"`
	Required   bool                        `json:"required,omitempty"`
	HasDefault bool                        `json:"hasDefault,omitempty"`
	Default    string                      `json:"default,omitempty"`
	Sortable   bool                        `json:"sortable,omitempty"`
	Identity   bool                        `json:"identity,omitempty"`
	Rules      []validation.RuleDescriptor `json:"rules,omitempty"`
	Presence   *FieldPresence              `json:"presence,omitempty"`
	Binding    *QueryBinding               `json:"binding,omitempty"`
	QueryRules *QueryRuleRepresentation    `json:"queryRules,omitempty"`
}

type EnumMember struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TypeDescriptor struct {
	Key           string            `json:"key"`
	Name          metadata.TypeName `json:"name"`
	Kind          string            `json:"kind"`
	Source        string            `json:"source"`
	Fields        []FieldDescriptor `json:"fields,omitempty"`
	Members       []EnumMember      `json:"members,omitempty"`
	Flags         bool              `json:"flags,omitempty"`
	EnumDomain    string            `json:"enumDomain,omitempty"`
	Import        *ImportMapping    `json:"import,omitempty"`
	Base          string            `json:"base,omitempty"`
	DerivedID     string            `json:"derivedId,omitempty"`
	Interface     string            `json:"interface,omitempty"`
	Derivatives   []string          `json:"derivatives,omitempty"`
	Scalar        *ScalarContract   `json:"scalar,omitempty"`
	Schemas       *WireSchemas      `json:"schemas,omitempty"`
	Discriminator *DerivedContract  `json:"discriminator,omitempty"`
	TSIncluded    *bool             `json:"tsIncluded,omitempty"`
}

type CommandDescriptor struct {
	Declaration   metadata.Command  `json:"declaration"`
	TypeKey       string            `json:"typeKey"`
	Source        string            `json:"source"`
	Fields        []FieldDescriptor `json:"fields,omitempty"`
	Input         *WireType         `json:"input,omitempty"`
	Response      *WireType         `json:"response,omitempty"`
	ResponseKind  string            `json:"responseKind"`
	Roles         []string          `json:"roles"`
	Excluded      bool              `json:"excluded"`
	PortableRules bool              `json:"portableRules,omitempty"`
}

type QueryDescriptor struct {
	Declaration   metadata.Query    `json:"declaration"`
	TypeKey       string            `json:"typeKey"`
	Source        string            `json:"source"`
	Parameters    []FieldDescriptor `json:"parameters,omitempty"`
	Result        WireType          `json:"result"`
	Delivery      string            `json:"delivery"`
	Paged         bool              `json:"paged"`
	SortFields    []string          `json:"sortFields"`
	ClientHTTP    string            `json:"clientHttp,omitempty"`
	DataPresence  string            `json:"dataPresence,omitempty"`
	Roles         []string          `json:"roles"`
	Excluded      bool              `json:"excluded"`
	PortableRules bool              `json:"portableRules,omitempty"`
}

func typeKey(t types.Type) string {
	return types.TypeString(t, func(pkg *types.Package) string { return pkg.Path() })
}

func buildGraph(analyses []*analysis, profile ApplicationProfile, wire bool) (*Graph, error) {
	if err := validateProfile(profile); err != nil {
		return nil, err
	}
	typescript := wire
	wire = wire || profile.OpenAPI != nil
	if !wire && (len(profile.WireSchemas) > 0 || len(profile.ResponseFields) > 0) {
		return nil, fmt.Errorf("schema/response assertions require a wire-contract consumer (TypeScript or internal OpenAPI analysis)")
	}
	graph := &Graph{FormatVersion: GraphVersion, Profile: profile, Catalog: metadata.Catalog{Version: metadata.Version}, verifyEndpoints: wire}
	if profile.FormatVersion == ContractGraphVersion {
		graph.FormatVersion = ContractGraphVersion
	}
	// Output locations are operational, not contract identity or machine provenance.
	graph.Profile.TypeScript.Out = ""
	if profile.OpenAPI != nil {
		copy := *profile.OpenAPI
		copy.Out = ""
		if len(copy.Servers) == 0 {
			copy.Servers = []string{"/"}
		}
		if copy.Streaming == "" {
			copy.Streaming = "error"
		}
		graph.Profile.OpenAPI = &copy
	}
	for _, a := range analyses {
		if namespace, ok := profile.PackageNamespaces[a.pkg.PkgPath]; ok {
			a.namespace = namespace
			a.staticNamespace = true
		} else if a.namespace == "" && profile.DefaultNamespace != nil {
			a.namespace = *profile.DefaultNamespace
			a.staticNamespace = true
		} else if a.namespace != "" {
			a.staticNamespace = true
		}
		if wire && !a.staticNamespace && len(a.commands)+len(a.models)+len(a.exports) > 0 {
			return nil, fmt.Errorf("%s: proxy generation requires a statically specified namespace default", a.pkg.PkgPath)
		}
		pkg := PackageDescriptor{GoPath: a.pkg.PkgPath, Namespace: a.namespace}
		for _, source := range a.pkg.GoFiles {
			if filepath.Base(source) != Filename {
				pkg.Sources = append(pkg.Sources, filepath.Base(source))
			}
		}
		sort.Strings(pkg.Sources)
		graph.Packages = append(graph.Packages, pkg)
		for i := range a.commands {
			c := &a.commands[i]
			declaration := metadata.Command{Type: metadata.TypeName{Namespace: a.namespace, Name: c.d.name}, Path: c.d.path, Authorization: c.d.auth, ExcludeFromDiscovery: c.d.exclude, BlockOnValidationSeverity: c.d.severity}
			graph.Catalog.Commands = append(graph.Catalog.Commands, declaration)
			descriptor := CommandDescriptor{Declaration: declaration, TypeKey: typeKey(c.typ), Source: filepath.Base(a.pkg.Fset.Position(c.pos).Filename), Roles: roles(c.d.auth), Excluded: excluded(profile, declaration.Type.Identity())}
			if c.handle.output == nil || namedType(c.handle.output, runtimePath+"/commands", "NoResponse") || namedType(c.handle.output, runtimePath+"/validation", "Result") || namedType(c.handle.output, runtimePath+"/authorization", "Decision") {
				descriptor.ResponseKind = "none"
			} else {
				descriptor.ResponseKind = "unknown"
			}
			override := c.d.response
			if configured, supplied := profile.Responses[declaration.Type.Identity()]; supplied {
				override = configured
			}
			if override != "" && override != "none" && override != "value" {
				return nil, diagnostic(a.pkg, c.pos, "response override must be none or value")
			}
			if override == "value" && c.handle.output == nil {
				return nil, diagnostic(a.pkg, c.pos, "void command cannot declare a value response")
			}
			graph.Commands = append(graph.Commands, descriptor)
			c.descriptor = &graph.Commands[len(graph.Commands)-1]
		}
		for i := range a.queries {
			q := &a.queries[i]
			declaration := metadata.Query{ReadModel: metadata.TypeName{Namespace: a.namespace, Name: q.model.d.name}, Name: q.d.name, Observable: q.emission != nil, ReadModelPath: q.model.d.path, ReadModelAuthorization: q.model.d.auth, Authorization: q.d.auth, HTTPMethod: metadata.QueryHTTPMethod(q.d.http), ExcludeFromDiscovery: q.d.exclude || q.model.d.exclude}
			if q.d.hasPath {
				value := q.d.path
				declaration.Path = &value
			}
			graph.Catalog.Queries = append(graph.Catalog.Queries, declaration)
			preference := profile.ClientHTTP[declaration.Identity()]
			if preference == "" && q.d.http == "QUERY" {
				preference = "Query"
			}
			if preference != "" && preference != "Get" && preference != "Query" && preference != "Auto" {
				return nil, diagnostic(a.pkg, q.call.decl.Pos(), "invalid client HTTP preference %q", preference)
			}
			if q.d.http == "QUERY" && (preference == "Get" || preference == "Auto") || preference == "Query" && (q.d.http == "GET" || !profile.routeOptions().EnableQueryHTTPMethod) {
				return nil, diagnostic(a.pkg, q.call.decl.Pos(), "client HTTP preference is incompatible with exposed endpoints")
			}
			delivery := "snapshot"
			if q.emission != nil {
				delivery = "observable"
			}
			descriptor := QueryDescriptor{Declaration: declaration, TypeKey: typeKey(q.model.typ), Source: filepath.Base(a.pkg.Fset.Position(q.call.decl.Pos()).Filename), ClientHTTP: preference, Delivery: delivery, Roles: roles(q.d.auth, q.model.d.auth), Excluded: excluded(profile, declaration.Identity())}
			graph.Queries = append(graph.Queries, descriptor)
			q.descriptor = &graph.Queries[len(graph.Queries)-1]
		}
	}
	if wire {
		commands, queries := map[string]bool{}, map[string]bool{}
		for _, declaration := range graph.Catalog.Commands {
			commands[declaration.Type.Identity()] = true
		}
		for _, declaration := range graph.Catalog.Queries {
			queries[declaration.Identity()] = true
		}
		var unknown []string
		for identity := range profile.Responses {
			if !commands[identity] {
				unknown = append(unknown, "response: "+identity)
			}
		}
		for identity := range profile.ClientHTTP {
			if !queries[identity] {
				unknown = append(unknown, "clientHttp: "+identity)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, fmt.Errorf("unknown profile artifact overrides: %s", strings.Join(unknown, ", "))
		}
	}
	endpoints, err := metadata.Resolve(graph.Catalog, profile.routeOptions())
	if err != nil {
		return nil, err
	}
	graph.Endpoints = endpoints
	// Attachments are relinked after slice growth; emitters never keep pointers
	// into intermediate append storage or independently reclassify declarations.
	for _, a := range analyses {
		for i := range a.commands {
			for j := range graph.Commands {
				if graph.Commands[j].TypeKey == typeKey(a.commands[i].typ) {
					a.commands[i].descriptor = &graph.Commands[j]
				}
			}
		}
		for i := range a.queries {
			for j := range graph.Queries {
				if graph.Queries[j].Declaration.Identity() == (metadata.TypeName{Namespace: a.namespace, Name: a.queries[i].model.d.name}).Identity()+"."+a.queries[i].d.name {
					a.queries[i].descriptor = &graph.Queries[j]
				}
			}
		}
		a.graph = graph
	}
	if wire {
		if err := analyzeWireGraph(graph, analyses, profile, typescript); err != nil {
			return nil, err
		}
	}
	if graph.FormatVersion == ContractGraphVersion {
		if err := validateResponseReferences(graph); err != nil {
			return nil, err
		}
	}
	if profile.Server != nil {
		assertions, err := normalizeAssertions(graph)
		if err != nil {
			return nil, err
		}
		graph.Assertions = assertions
	}
	projection, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(projection)
	graph.Fingerprint = hex.EncodeToString(fingerprint[:])
	return graph, nil
}

func roles(declarations ...*metadata.Authorization) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, declaration := range declarations {
		if declaration == nil {
			continue
		}
		for _, requirement := range declaration.Requirements {
			for _, role := range requirement.Roles {
				if !seen[role] {
					seen[role] = true
					result = append(result, role)
				}
			}
		}
	}
	return result
}

func excluded(profile ApplicationProfile, identity string) bool {
	for _, value := range profile.TypeScript.ExcludeTypes {
		if identity == value {
			return true
		}
	}
	for _, value := range profile.TypeScript.ExcludeNamespaces {
		value = strings.TrimSuffix(value, ".*")
		if identity == value || strings.HasPrefix(identity, value+".") {
			return true
		}
	}
	return false
}
