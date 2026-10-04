// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/cratis/arc.go/metadata"
)

const openAPIDialect = "https://json-schema.org/draft/2020-12/schema"

// openAPIDocument is a copy-owned internal serialization checkpoint, not a
// publication-ready document or a replacement for the exact validation gate.
// It never holds graph slices, caller schemas, compiler objects or a kin view.
// The zero value is invalid. No generator calls this checkpoint.
type openAPIDocument struct{ data []byte }

func (d openAPIDocument) bytes() []byte { return bytes.Clone(d.data) }

type openAPIObject map[string]any

type openAPIRenderer struct {
	graph      *Graph
	names      map[string]string
	framework  map[string]FrameworkContract
	components openAPIObject
}

// renderOpenAPI borrows a finalized graph for this synchronous call. Errors
// return no document, including when one later artifact is unsupported. This
// deliberately small profile refuses opaque schemas, specialized codecs,
// authorization, query arguments/paging, framework routes and streams rather
// than guessing at their contracts. Full validation/publication is a later seam.
func renderOpenAPI(graph *Graph) (openAPIDocument, error) {
	if err := admitOpenAPIGraph(graph); err != nil {
		return openAPIDocument{}, err
	}
	r := &openAPIRenderer{graph: graph, names: map[string]string{}, framework: map[string]FrameworkContract{}, components: openAPIObject{}}
	nodes := slices.Clone(graph.Types)
	slices.SortFunc(nodes, func(a, b TypeDescriptor) int { return strings.Compare(a.Key, b.Key) })
	identities := map[string]bool{}
	for _, node := range nodes {
		identity := node.Name.Identity()
		if node.Key == "" || node.Name.Name == "" || r.names[node.Key] != "" || identities[identity] {
			return openAPIDocument{}, fmt.Errorf("openapi: duplicate or empty type identity %q (%s)", identity, node.Key)
		}
		identities[identity] = true
		r.names[node.Key] = "Model." + openAPIName(identity)
	}
	for _, contract := range graph.Framework {
		if _, exists := r.framework[contract.Key]; exists || contract.Key == "" {
			return openAPIDocument{}, fmt.Errorf("openapi: duplicate or empty framework identity %q", contract.Key)
		}
		r.framework[contract.Key] = contract
	}
	for _, node := range nodes {
		for _, direction := range []string{"Input", "Output"} {
			schema, err := r.model(node, direction)
			if err != nil {
				return openAPIDocument{}, fmt.Errorf("openapi: %s.%s: %w", node.Key, direction, err)
			}
			r.components[r.names[node.Key]+"."+direction] = schema
		}
	}
	for _, key := range []string{"Cratis.ValidationResult", "Cratis.PagingInfo"} {
		contract, exists := r.framework[key]
		if !exists {
			return openAPIDocument{}, fmt.Errorf("openapi: missing framework contract %s", key)
		}
		schema, err := r.fields(contract.Fields, "Output", true)
		if err != nil {
			return openAPIDocument{}, fmt.Errorf("openapi: %s: %w", key, err)
		}
		r.components[key] = schema
	}
	paths, err := r.paths()
	if err != nil {
		return openAPIDocument{}, err
	}
	servers := make([]openAPIObject, len(graph.Profile.OpenAPI.Servers))
	for i, server := range graph.Profile.OpenAPI.Servers {
		// This checkpoint admits only the source-backed relative deployment root.
		// General URI admission belongs to the reviewed validator integration.
		if server != "/" {
			return openAPIDocument{}, fmt.Errorf("openapi: server URL outside checkpoint profile: %q", server)
		}
		servers[i] = openAPIObject{"url": server}
	}
	if len(servers) == 0 {
		servers = []openAPIObject{{"url": "/"}}
	}
	document := openAPIObject{
		"openapi": "3.1.1", "jsonSchemaDialect": openAPIDialect,
		"info":    openAPIObject{"title": graph.Profile.OpenAPI.Title, "version": graph.Profile.OpenAPI.Version},
		"servers": servers, "paths": paths, "components": openAPIObject{"schemas": r.components},
		"x-cratis-profile": openAPIObject{"provenance": "application-profile-assertion", "coverage": "application-operations", "securityVerified": false, "schemaVerified": false, "query": "Cratis-aware consumer required"},
	}
	data, err := json.Marshal(document)
	if err != nil {
		return openAPIDocument{}, fmt.Errorf("openapi: encode: %w", err)
	}
	return openAPIDocument{data: data}, nil
}

func admitOpenAPIGraph(graph *Graph) error {
	if graph == nil || graph.FormatVersion != ContractGraphVersion || graph.Profile.OpenAPI == nil || graph.Profile.Server == nil || graph.Assertions == nil {
		return fmt.Errorf("openapi: finalized v2 graph with explicit OpenAPI/server assertions required")
	}
	if err := validateProfile(graph.Profile); err != nil {
		return fmt.Errorf("openapi: profile: %w", err)
	}
	if graph.Profile.OpenAPI.IncludeFrameworkEndpoints || graph.Profile.OpenAPI.Streaming != "" && graph.Profile.OpenAPI.Streaming != "error" {
		return fmt.Errorf("openapi: framework routes and streaming metadata are outside checkpoint profile")
	}
	if len(graph.Diagnostics) != 0 || len(graph.Profile.WireSchemas) != 0 {
		return fmt.Errorf("openapi: diagnostics and opaque wire schemas require the exact validator integration")
	}
	if len(graph.Profile.ResponseFields) != 1 || len(graph.Profile.ResponseFields["Cratis.ValidationResult"]) != 1 || !graph.Profile.ResponseFields["Cratis.ValidationResult"]["state"].Absent {
		return fmt.Errorf("openapi: checkpoint requires exactly the absent validation state assertion")
	}
	server := graph.Profile.Server
	if len(server.Authentication.Schemes) != 0 || len(server.Authentication.Handlers) != 0 || server.Authorization.Fallback != nil || len(server.Authorization.Policies) != 0 || server.Identity.DetailsType != "" {
		return fmt.Errorf("openapi: authentication, authorization and identity customizations require a separate witnessed profile")
	}
	assertions, err := normalizeAssertions(graph)
	if err != nil {
		return fmt.Errorf("openapi: normalize assertions: %w", err)
	}
	// The private analysis flag is not serialized. It must not turn a decoded
	// graph into a different contract or establish runtime verification here.
	assertions.RouteVerification = graph.Assertions.RouteVerification
	if !reflect.DeepEqual(assertions, graph.Assertions) {
		return fmt.Errorf("openapi: inconsistent normalized server assertions")
	}
	expectedFramework, err := frameworkContracts(graph, nil)
	if err != nil {
		return fmt.Errorf("openapi: framework contracts: %w", err)
	}
	actualFramework := slices.Clone(graph.Framework)
	orderFramework := func(values []FrameworkContract) {
		slices.SortFunc(values, func(a, b FrameworkContract) int { return strings.Compare(a.Key, b.Key) })
	}
	orderFramework(expectedFramework)
	orderFramework(actualFramework)
	if !reflect.DeepEqual(actualFramework, expectedFramework) {
		return fmt.Errorf("openapi: framework inventory differs from the admitted absent-state profile")
	}
	// Resolve is the existing shared route authority, not a renderer router.
	resolved, err := metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		return fmt.Errorf("openapi: routes: %w", err)
	}
	actual := slices.Clone(graph.Endpoints)
	sortEndpoints := func(endpoints []metadata.Endpoint) {
		slices.SortFunc(endpoints, func(a, b metadata.Endpoint) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
	}
	sortEndpoints(resolved)
	sortEndpoints(actual)
	if !reflect.DeepEqual(resolved, actual) {
		return fmt.Errorf("openapi: endpoint inventory disagrees with shared route authority")
	}
	return nil
}

// Encode every non-ASCII component byte and underscore to keep names injective.
func openAPIName(identity string) string {
	var name strings.Builder
	for i := 0; i < len(identity); i++ {
		c := identity[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' {
			name.WriteByte(c)
		} else {
			fmt.Fprintf(&name, "_%02X", c)
		}
	}
	return name.String()
}

func openAPIRef(name string) openAPIObject {
	return openAPIObject{"$ref": "#/components/schemas/" + name}
}

func openAPINull(schema openAPIObject, nullable bool) openAPIObject {
	if nullable {
		return openAPIObject{"anyOf": []any{schema, openAPIObject{"type": "null"}}}
	}
	return schema
}
