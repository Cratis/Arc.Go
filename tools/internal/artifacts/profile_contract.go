// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/metadata"
)

// OpenAPIProfile requests contract analysis, not document publication in this checkpoint.
type OpenAPIProfile struct {
	Out                       string   `json:"out,omitempty"`
	Title                     string   `json:"title"`
	Version                   string   `json:"version"`
	Servers                   []string `json:"servers,omitempty"`
	IncludeFrameworkEndpoints bool     `json:"includeFrameworkEndpoints,omitempty"`
	Streaming                 string   `json:"streaming,omitempty"`
}

// ServerProfile is an application assertion. It is never inferred from Builder
// calls, generated endpoint expectations, middleware or application execution.
type ServerProfile struct {
	Runtime        string                `json:"runtime"`
	Environment    string                `json:"environment,omitempty"`
	HTTP           HTTPProfile           `json:"http,omitempty"`
	Authentication AuthenticationProfile `json:"authentication,omitempty"`
	Authorization  AuthorizationProfile  `json:"authorization,omitempty"`
	Introspection  IntrospectionProfile  `json:"introspection,omitempty"`
	Identity       IdentityProfile       `json:"identity,omitempty"`
}

// HTTPProfile describes only builtin request readers and unary publication limits.
type HTTPProfile struct {
	CorrelationHeader string `json:"correlationHeader,omitempty"`
	QueryReaders      string `json:"queryReaders,omitempty"`
	MaxBodyBytes      int64  `json:"maxBodyBytes,omitempty"`
	MaxQueryBytes     int    `json:"maxQueryBytes,omitempty"`
	MaxResponseBytes  int64  `json:"maxResponseBytes,omitempty"`
}

// AuthenticationProfile associates registered handlers, in order, with document schemes.
type AuthenticationProfile struct {
	Schemes  map[string]SecurityScheme `json:"schemes,omitempty"`
	Handlers []string                  `json:"handlers,omitempty"`
}

// SecurityScheme is the bounded standard credential-description subset supported
// by contract analysis. OAuth flows require a later explicit declaration boundary;
// unsupported fields fail decoding instead of silently claiming support.
type SecurityScheme struct {
	Type             string `json:"type"`
	Description      string `json:"description,omitempty"`
	Name             string `json:"name,omitempty"`
	In               string `json:"in,omitempty"`
	Scheme           string `json:"scheme,omitempty"`
	BearerFormat     string `json:"bearerFormat,omitempty"`
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`
}

// AuthorizationProfile asserts fallback declarations and policy guest capability.
type AuthorizationProfile struct {
	Fallback *metadata.Authorization  `json:"fallback,omitempty"`
	Policies map[string]PolicyProfile `json:"policies,omitempty"`
}

// PolicyProfile requires an explicit guest-capability assertion, even when false.
type PolicyProfile struct {
	EvaluatesAnonymous *bool `json:"evaluatesAnonymous"`
}

// IntrospectionProfile preserves omitted exposure settings separately from false.
type IntrospectionProfile struct {
	Enabled               *bool    `json:"enabled,omitempty"`
	RequireAuthentication *bool    `json:"requireAuthentication,omitempty"`
	Roles                 []string `json:"roles,omitempty"`
}

// IdentityProfile names the exact custom details wire type; empty means built-in null details.
type IdentityProfile struct {
	DetailsType string `json:"detailsType,omitempty"`
}

// WireSchemas supplies both directions for an opaque Go type. Raw JSON retains
// exact numeric tokens. This checkpoint checks declarations/references, not full
// JSON Schema validity; the future renderer must validate them before publication.
type WireSchemas struct {
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
}

// ResponseField asserts absence or exactly one type/schema for an opaque field.
type ResponseField struct {
	Absent bool            `json:"absent,omitempty"`
	Type   string          `json:"type,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

// ProfileAssertions records provenance and the limits of generated verification.
type ProfileAssertions struct {
	Provenance           string        `json:"provenance"`
	RouteVerification    string        `json:"routeVerification"`
	SecurityVerification string        `json:"securityVerification"`
	SchemaVerification   string        `json:"schemaVerification"`
	Server               ServerProfile `json:"server"`
	DiscoveryExposure    string        `json:"discoveryExposure"`
}

func validateContractProfile(profile ApplicationProfile) error {
	if p := profile.OpenAPI; p != nil {
		if profile.Server == nil {
			return fmt.Errorf("openapi requires explicit server assertions")
		}
		if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Version) == "" {
			return fmt.Errorf("openapi requires title and version")
		}
		if p.Streaming != "" && p.Streaming != "error" && p.Streaming != "metadata" {
			return fmt.Errorf("unsupported openapi streaming contract %q", p.Streaming)
		}
		for _, server := range p.Servers {
			parsed, err := url.Parse(server)
			if err != nil || server == "" || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.User != nil || parsed.Scheme != "" && parsed.Scheme != "https" && parsed.Scheme != "http" {
				return fmt.Errorf("invalid openapi server %q", server)
			}
		}
	}
	for _, key := range sortedKeys(profile.WireSchemas) {
		schemas := profile.WireSchemas[key]
		if key == "" {
			return fmt.Errorf("wireSchemas requires exact Go type keys")
		}
		if err := validateSchemaAssertion(schemas.Input); err != nil {
			return fmt.Errorf("wireSchemas %s input: %w", key, err)
		}
		if err := validateSchemaAssertion(schemas.Output); err != nil {
			return fmt.Errorf("wireSchemas %s output: %w", key, err)
		}
	}
	for _, identity := range sortedKeys(profile.ResponseFields) {
		for _, path := range sortedKeys(profile.ResponseFields[identity]) {
			field := profile.ResponseFields[identity][path]
			count := 0
			if field.Absent {
				count++
			}
			if field.Type != "" {
				count++
			}
			if len(field.Schema) > 0 {
				count++
			}
			if identity == "" || path == "" || count != 1 {
				return fmt.Errorf("responseFields %s.%s requires exactly one absent/type/schema declaration", identity, path)
			}
			if len(field.Schema) > 0 {
				if err := validateSchemaAssertion(field.Schema); err != nil {
					return fmt.Errorf("responseFields %s.%s: %w", identity, path, err)
				}
			}
		}
	}
	if p := profile.Server; p != nil {
		if p.Runtime != "arc-go" {
			return fmt.Errorf("server.runtime must be arc-go")
		}
		h := p.HTTP
		if h.QueryReaders != "" && h.QueryReaders != "builtin" {
			return fmt.Errorf("custom query readers require an explicit binding declaration; only builtin is supported")
		}
		if h.MaxBodyBytes < 0 || h.MaxQueryBytes < 0 || h.MaxResponseBytes < 0 {
			return fmt.Errorf("server HTTP limits must not be negative")
		}
		if h.CorrelationHeader != "" {
			for _, c := range h.CorrelationHeader {
				valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)
				if !valid {
					return fmt.Errorf("invalid server correlationHeader")
				}
			}
			for _, reserved := range []string{"Content-Type", "Content-Length", "Cache-Control", "Vary", "Allow", "Set-Cookie", "Authorization", "X-Allowed-Severity", "x-cratis-tenant-id", "Content-Encoding", "Connection", "Transfer-Encoding", "Host"} {
				if strings.EqualFold(h.CorrelationHeader, reserved) {
					return fmt.Errorf("reserved server correlationHeader %q", h.CorrelationHeader)
				}
			}
		}
		for _, role := range p.Introspection.Roles {
			if strings.TrimSpace(role) == "" {
				return fmt.Errorf("server introspection roles must be nonempty")
			}
		}
		for _, name := range sortedKeys(p.Authentication.Schemes) {
			scheme := p.Authentication.Schemes[name]
			valid := name != ""
			switch scheme.Type {
			case "http":
				valid = valid && scheme.Scheme != "" && scheme.Name == "" && scheme.In == "" && scheme.OpenIDConnectURL == "" && (scheme.BearerFormat == "" || strings.EqualFold(scheme.Scheme, "bearer"))
			case "apiKey":
				valid = valid && scheme.Name != "" && (scheme.In == "header" || scheme.In == "query" || scheme.In == "cookie") && scheme.Scheme == "" && scheme.BearerFormat == "" && scheme.OpenIDConnectURL == ""
			case "openIdConnect":
				u, err := url.Parse(scheme.OpenIDConnectURL)
				valid = valid && err == nil && u != nil && u.Scheme == "https" && u.Host != "" && scheme.Name == "" && scheme.In == "" && scheme.Scheme == "" && scheme.BearerFormat == ""
			default:
				valid = false
			}
			if !valid {
				return fmt.Errorf("invalid or unsupported server security scheme %q", name)
			}
		}
		seen := map[string]bool{}
		for _, handler := range p.Authentication.Handlers {
			if _, exists := p.Authentication.Schemes[handler]; !exists {
				return fmt.Errorf("unknown server authentication scheme reference %q", handler)
			}
			if seen[handler] {
				return fmt.Errorf("duplicate server authentication handler %q", handler)
			}
			seen[handler] = true
		}
		for _, name := range sortedKeys(p.Authorization.Policies) {
			if name == "" || p.Authorization.Policies[name].EvaluatesAnonymous == nil {
				return fmt.Errorf("server policy %q requires explicit evaluatesAnonymous", name)
			}
		}
		enforcement := len(p.Authentication.Handlers) > 0
		introspection := p.Introspection
		if len(introspection.Roles) > 0 && introspection.RequireAuthentication != nil && !*introspection.RequireAuthentication {
			return fmt.Errorf("server introspection roles contradict requireAuthentication=false")
		}
		if !enforcement && (len(introspection.Roles) > 0 || introspection.RequireAuthentication != nil && *introspection.RequireAuthentication) {
			return fmt.Errorf("server introspection authentication/roles require authentication handlers")
		}
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// validateSchemaAssertion is intentionally not a schema DSL or document validator.
// Unconstrained assertions and external references cannot establish a wire shape.
func validateSchemaAssertion(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		return fmt.Errorf("explicit object schema required: %w", err)
	}
	if len(schema) == 0 {
		return fmt.Errorf("unconstrained schema is not a wire contract")
	}
	if _, exists := schema["nullable"]; exists {
		return fmt.Errorf("nullable is not a JSON Schema 3.1 null union")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing schema assertion")
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "not", "if", "then", "else"} {
		if _, exists := schema[key]; exists {
			return fmt.Errorf("composite root schema assertions require the renderer validation checkpoint")
		}
	}
	if !validSchemaTypes(schema["type"]) {
		return fmt.Errorf("schema requires explicit type or type union; composite/reference-only assertions need later schema validation, no implicit any")
	}
	var walk func(any) error
	walk = func(value any) error {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if key == "$ref" {
					return fmt.Errorf("schema references require document-local resolution in the renderer checkpoint; unresolved references are not a wire assertion")
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range value {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(schema)
}

// assertionPolicy cannot evaluate. Registry.Build validates declarations only;
// this object is never an application policy or proof of authorization behavior.
type assertionPolicy struct{}

func (assertionPolicy) Authorize(context.Context, authorization.Context) (authorization.Decision, error) {
	return authorization.Decision{}, fmt.Errorf("profile assertions cannot evaluate authorization")
}

func validSchemaTypes(value any) bool {
	valid := func(value any) bool {
		text, ok := value.(string)
		return ok && (text == "string" || text == "number" || text == "integer" || text == "boolean" || text == "object" || text == "array" || text == "null")
	}
	if values, ok := value.([]any); ok {
		if len(values) == 0 {
			return false
		}
		seen := map[any]bool{}
		for _, value := range values {
			if !valid(value) || seen[value] {
				return false
			}
			seen[value] = true
		}
		return true
	}
	return valid(value)
}

func schemaAcceptsNull(raw json.RawMessage) bool {
	var schema map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		return false
	}
	if value, exists := schema["const"]; exists && value != nil {
		return false
	}
	if values, ok := schema["enum"].([]any); ok {
		found := false
		for _, value := range values {
			if value == nil {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if schema["type"] == "null" {
		return true
	}
	if values, ok := schema["type"].([]any); ok {
		for _, value := range values {
			if value == "null" {
				return true
			}
		}
	}
	return false
}

func normalizeAssertions(graph *Graph) (*ProfileAssertions, error) {
	server := *graph.Profile.Server
	registry := &authorization.Registry{}
	for _, name := range sortedKeys(server.Authorization.Policies) {
		if err := registry.Register(name, assertionPolicy{}, authorization.PolicyOptions{EvaluatesAnonymous: *server.Authorization.Policies[name].EvaluatesAnonymous}); err != nil {
			return nil, fmt.Errorf("server policy assertion: %w", err)
		}
	}
	if _, err := registry.Build(graph.Catalog, authorization.Options{Fallback: server.Authorization.Fallback}); err != nil {
		return nil, fmt.Errorf("server authorization assertion: %w", err)
	}
	if server.Environment == "" {
		server.Environment = "Production"
	}
	if server.HTTP.CorrelationHeader == "" {
		server.HTTP.CorrelationHeader = "X-Correlation-ID"
	}
	if server.HTTP.QueryReaders == "" {
		server.HTTP.QueryReaders = "builtin"
	}
	if server.HTTP.MaxBodyBytes == 0 {
		server.HTTP.MaxBodyBytes = 1 << 20
	}
	if server.HTTP.MaxQueryBytes == 0 {
		server.HTTP.MaxQueryBytes = 8 << 10
	}
	if server.HTTP.MaxResponseBytes == 0 {
		server.HTTP.MaxResponseBytes = 16 << 20
	}
	exposure := "unmapped"
	authenticated := server.Introspection.RequireAuthentication
	if authenticated != nil && *authenticated || len(server.Introspection.Roles) > 0 || authenticated == nil && server.Environment != "Development" && len(server.Authentication.Handlers) > 0 {
		exposure = "authenticated"
	} else if authenticated != nil && !*authenticated || server.Environment == "Development" {
		exposure = "anonymous"
	}
	routeVerification := "not-requested"
	if graph.verifyEndpoints {
		routeVerification = "generated-endpoint-expectations"
	}
	return &ProfileAssertions{Provenance: "application-profile-assertion", RouteVerification: routeVerification, SecurityVerification: "not-verified-by-route-expectations", SchemaVerification: "not-verified-by-route-expectations", Server: server, DiscoveryExposure: exposure}, nil
}

func validateResponseReferences(graph *Graph) error {
	identities := map[string]bool{}
	commands := map[string]CommandDescriptor{}
	for _, command := range graph.Commands {
		identity := command.Declaration.Type.Identity()
		identities[identity] = true
		commands[identity] = command
		if field, exists := graph.Profile.ResponseFields[identity]["response"]; exists {
			if command.ResponseKind == "none" && !field.Absent || command.ResponseKind == "value" && field.Absent {
				return fmt.Errorf("responseFields %s.response contradicts known response presence", identity)
			}
			if command.Response != nil && command.Response.Kind != "declared" && (field.Type != "" && command.Response.Contract != nil && field.Type != command.Response.Contract.Declared || len(field.Schema) > 0) {
				return fmt.Errorf("responseFields %s.response contradicts typed response; use its exact type or wireSchemas", identity)
			}
		}
	}
	for _, query := range graph.Queries {
		identities[query.Declaration.Identity()] = true
	}
	for _, identity := range sortedKeys(graph.Profile.ResponseFields) {
		if !identities[identity] && identity != "Cratis.ValidationResult" && identity != "Cratis.Identity" && identity != "Cratis.Discovery" {
			return fmt.Errorf("unknown responseFields artifact reference %q", identity)
		}
		for _, path := range sortedKeys(graph.Profile.ResponseFields[identity]) {
			_, command := commands[identity]
			allowed := command && path == "response" || identities[identity] && path == "validationResults.state" || identity == "Cratis.ValidationResult" && path == "state" || identity == "Cratis.Identity" && path == "details" || identity == "Cratis.Discovery" && path == "schema"
			if !allowed {
				return fmt.Errorf("unknown or unsupported responseFields path %s.%s", identity, path)
			}
		}
	}
	if graph.Profile.Server != nil {
		if key := graph.Profile.Server.Identity.DetailsType; key != "" {
			if _, exists := graph.Profile.WireSchemas[key]; !exists {
				return fmt.Errorf("identity detailsType %q requires explicit input/output wireSchemas", key)
			}
		}
	}
	return nil
}
