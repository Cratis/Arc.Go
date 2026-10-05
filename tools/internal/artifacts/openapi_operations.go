// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/cratis/arc.go/metadata"
)

func (r *openAPIRenderer) paths() (openAPIObject, error) {
	commands := map[string]CommandDescriptor{}
	queries := map[string]QueryDescriptor{}
	catalogCommands := map[string]metadata.Command{}
	catalogQueries := map[string]metadata.Query{}
	for _, declaration := range r.graph.Catalog.Commands {
		catalogCommands[declaration.Type.Identity()] = declaration
	}
	for _, declaration := range r.graph.Catalog.Queries {
		catalogQueries[declaration.Identity()] = declaration
	}
	orderedCommands := slices.Clone(r.graph.Commands)
	slices.SortFunc(orderedCommands, func(a, b CommandDescriptor) int {
		return strings.Compare(a.Declaration.Type.Identity(), b.Declaration.Type.Identity())
	})
	for _, command := range orderedCommands {
		identity := command.Declaration.Type.Identity()
		if _, exists := commands[identity]; exists || !reflect.DeepEqual(command.Declaration, catalogCommands[identity]) {
			return nil, fmt.Errorf("openapi: duplicate or inconsistent command %s", identity)
		}
		if command.Declaration.ExcludeFromDiscovery || command.Declaration.Authorization != nil || len(command.Roles) > 0 || command.Declaration.BlockOnValidationSeverity != nil || command.PortableRules {
			return nil, fmt.Errorf("openapi: %s: exclusion, authorization and validation customization are outside checkpoint profile", identity)
		}
		if command.Input == nil || command.Input.Target != command.TypeKey || command.ResponseKind != "none" && command.ResponseKind != "value" || (command.Response == nil) != (command.ResponseKind == "none") {
			return nil, fmt.Errorf("openapi: %s: inconsistent command input/response contract", identity)
		}
		commands[identity] = command
	}
	orderedQueries := slices.Clone(r.graph.Queries)
	slices.SortFunc(orderedQueries, func(a, b QueryDescriptor) int {
		return strings.Compare(a.Declaration.Identity(), b.Declaration.Identity())
	})
	for _, query := range orderedQueries {
		identity := query.Declaration.Identity()
		if _, exists := queries[identity]; exists || !reflect.DeepEqual(query.Declaration, catalogQueries[identity]) {
			return nil, fmt.Errorf("openapi: duplicate or inconsistent query %s", identity)
		}
		if query.Declaration.ExcludeFromDiscovery || query.Declaration.Authorization != nil || query.Declaration.ReadModelAuthorization != nil || len(query.Roles) > 0 || query.PortableRules {
			return nil, fmt.Errorf("openapi: %s: exclusion, authorization and validation customization are outside checkpoint profile", identity)
		}
		if query.Delivery != "snapshot" || query.Declaration.Observable || query.Paged || len(query.SortFields) > 0 || len(query.Parameters) > 0 || query.DataPresence != "omit-nil-data" {
			return nil, fmt.Errorf("openapi: %s: only nonpaged, argument-free snapshot queries are admitted", identity)
		}
		queries[identity] = query
	}
	if len(commands) != len(catalogCommands) || len(queries) != len(catalogQueries) {
		return nil, fmt.Errorf("openapi: descriptor and catalog inventories differ")
	}
	endpoints := slices.Clone(r.graph.Endpoints)
	slices.SortFunc(endpoints, func(a, b metadata.Endpoint) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
	paths := openAPIObject{}
	operationIDs := map[string]bool{}
	for _, endpoint := range endpoints {
		operation, err := r.operation(endpoint, commands, queries)
		if err != nil {
			return nil, fmt.Errorf("openapi: %s %s: %w", endpoint.Method, endpoint.Path, err)
		}
		if operationIDs[operation["operationId"].(string)] {
			return nil, fmt.Errorf("openapi: duplicate operation ID %s", operation["operationId"])
		}
		operationIDs[operation["operationId"].(string)] = true
		item, exists := paths[endpoint.Path].(openAPIObject)
		if !exists {
			item = openAPIObject{}
			paths[endpoint.Path] = item
		}
		if endpoint.Method == "QUERY" {
			// QUERY is not a native OpenAPI 3.1 method. Keep the actual operation
			// at its original extension pointer; never substitute GET or POST.
			item["x-cratis-query"] = openAPIObject{"method": "QUERY", "operation": operation}
		} else {
			item[strings.ToLower(endpoint.Method)] = operation
		}
		if endpoint.Method == "GET" {
			head := endpoint
			head.Method = "HEAD"
			operation, err := r.operation(head, commands, queries)
			if err != nil {
				return nil, err
			}
			item["head"] = operation
		}
	}
	return paths, nil
}

func (r *openAPIRenderer) operation(endpoint metadata.Endpoint, commands map[string]CommandDescriptor, queries map[string]QueryDescriptor) (openAPIObject, error) {
	var input, payload *WireType
	category, framework := "Query", "Cratis.QueryResult"
	summary := ""
	if endpoint.Method == "POST" {
		command, exists := commands[endpoint.Identity]
		if !exists {
			return nil, fmt.Errorf("unresolved command")
		}
		category, framework = "Execute", "Cratis.CommandResult"
		input, payload, summary = command.Input, command.Response, command.Declaration.DocumentationSummary
		if endpoint.ValidateOnly {
			category, framework, payload = "Validate", "Cratis.CommandValidationResult", nil
		}
	} else {
		query, exists := queries[endpoint.Identity]
		if !exists || endpoint.ValidateOnly || (endpoint.Method != "GET" && endpoint.Method != "HEAD" && endpoint.Method != "QUERY") {
			return nil, fmt.Errorf("unsupported or unresolved query endpoint")
		}
		payload, summary = &query.Result, query.Declaration.DocumentationSummary
	}
	contract, exists := r.framework[framework]
	if !exists {
		return nil, fmt.Errorf("missing framework contract %s", framework)
	}
	schema, err := r.fields(contract.Fields, "Output", true)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		if contract.Payload == "" {
			return nil, fmt.Errorf("missing framework payload contract")
		}
		value, err := r.wire(*payload, "Output", false, false, 0)
		if err != nil {
			return nil, err
		}
		schema["properties"].(openAPIObject)[contract.Payload] = value
		if category == "Execute" {
			// Command response is omitted on failure, including a nonnull payload.
			schema["if"] = openAPIObject{"properties": openAPIObject{"isSuccess": openAPIObject{"const": false}}, "required": []string{"isSuccess"}}
			schema["then"] = openAPIObject{"not": openAPIObject{"required": []string{contract.Payload}}}
		}
	}
	name := "Operation." + openAPIName(endpoint.Identity) + "." + category
	if previous, exists := r.components[name]; exists && !reflect.DeepEqual(previous, schema) {
		return nil, fmt.Errorf("operation component collision %s", name)
	}
	r.components[name] = schema
	operation := openAPIObject{
		"operationId": category + "." + endpoint.Identity + "." + endpoint.Method,
		"security":    []any{},
		"responses":   r.responses(endpoint, openAPIRef(name)),
		"parameters":  []any{openAPIObject{"name": r.graph.Assertions.Server.HTTP.CorrelationHeader, "in": "header", "required": false, "description": "A valid UUID is reused; other text is replaced with a generated UUID.", "schema": openAPIObject{"type": "string"}}},
	}
	if summary != "" {
		operation["summary"] = summary
	}
	if endpoint.Method == "QUERY" {
		operation["requestBody"] = openAPIObject{"required": true, "content": openAPIObject{"application/json": openAPIObject{"schema": openAPIQueryRequest()}}}
		operation["description"] = "HTTP QUERY with the built-in JSON request reader. OpenAPI 3.1 consumers must explicitly understand x-cratis-query to invoke this operation; it is not a GET or POST alias."
	}
	if input != nil {
		body, err := r.wire(*input, "Input", false, false, 0)
		if err != nil {
			return nil, err
		}
		operation["requestBody"] = openAPIObject{"required": true, "content": openAPIObject{"application/json": openAPIObject{"schema": body}}}
		operation["parameters"] = append(operation["parameters"].([]any), openAPIObject{"name": "X-Allowed-Severity", "in": "header", "schema": openAPIObject{"type": "string"}, "description": "Controls the request validation severity floor; malformed values use the configured default."})
	}
	return operation, nil
}

func (r *openAPIRenderer) responses(endpoint metadata.Endpoint, schema openAPIObject) openAPIObject {
	responses := openAPIObject{}
	descriptions := map[string]string{"200": "Success", "400": "Invalid request or validation failure", "403": "Pipeline authorization denied", "413": "Request exceeds the configured size limit", "500": "Internal error; emergency publication failures may have an empty body", "503": "Application is not admitting requests; empty body"}
	if endpoint.Method == "POST" || endpoint.Method == "QUERY" {
		descriptions["415"] = "Unsupported request representation"
	}
	for status, description := range descriptions {
		response := openAPIObject{"description": description,
			"headers": openAPIObject{r.graph.Assertions.Server.HTTP.CorrelationHeader: openAPIObject{"schema": openAPIObject{"type": "string", "format": "uuid"}}},
		}
		if endpoint.Method == "QUERY" && status != "503" {
			response["headers"].(openAPIObject)["Cache-Control"] = openAPIObject{"schema": openAPIObject{"type": "string", "const": "no-store"}}
		}
		if status != "503" && endpoint.Method != "HEAD" {
			response["content"] = openAPIObject{"application/json": openAPIObject{"schema": schema}}
		}
		if status == "500" || status == "503" {
			response["x-cratis-empty-body-possible"] = true
		}
		responses[status] = response
	}
	return responses
}
