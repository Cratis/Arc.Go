// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/cratis/arc.go/queries"
)

type commandCatalogEntry struct {
	Name                 string `json:"name"`
	Namespace            string `json:"namespace"`
	Route                string `json:"route"`
	Type                 string `json:"type"`
	DocumentationSummary string `json:"documentationSummary"`
	PayloadSchema        any    `json:"payloadSchema"`
}
type queryCatalogEntry struct {
	Name                 string `json:"name"`
	Namespace            string `json:"namespace"`
	Route                string `json:"route"`
	FullyQualifiedName   string `json:"fullyQualifiedName"`
	Type                 string `json:"type"`
	DocumentationSummary string `json:"documentationSummary"`
	ArgumentsSchema      any    `json:"argumentsSchema"`
}

func (a *Application) compileCatalogs() error {
	a.catalogJSON = make(map[string]json.RawMessage)
	if a.discovery == discoveryUnavailable {
		return nil
	}
	identitySchema := any(map[string]any{})
	if a.details.name != "" {
		var err error
		identitySchema, err = a.schema(a.details.typ)
		if err != nil {
			return err
		}
	}
	body, err := json.Marshal(identitySchema)
	if err != nil {
		return err
	}
	a.catalogJSON["/.cratis/identity-details/schema"] = body
	if a.options.Introspection.Enabled != nil && !*a.options.Introspection.Enabled {
		return nil
	}
	commands := []commandCatalogEntry{}
	queryEntries := []queryCatalogEntry{}
	for _, c := range a.catalog.Commands {
		if c.ExcludeFromDiscovery {
			continue
		}
		r, _ := a.commands.Lookup(c.Type.Identity())
		schema, err := a.schema(r.CommandType())
		if err != nil {
			return &ConfigurationError{Component: "command schema", Name: c.Type.Identity(), Cause: err}
		}
		route := ""
		for _, e := range a.endpoints {
			if e.Identity == c.Type.Identity() && !e.ValidateOnly {
				route = e.Path
				break
			}
		}
		commands = append(commands, commandCatalogEntry{c.Type.Name, a.catalogNamespace(c.Type.Namespace), route, c.Type.Identity(), c.DocumentationSummary, schema})
	}
	for _, q := range a.catalog.Queries {
		if q.ExcludeFromDiscovery {
			continue
		}
		r, _ := a.queries.Lookup(queries.FullyQualifiedQueryName(q.Identity()))
		properties := map[string]any{}
		required := []string{}
		for _, parameter := range r.Parameters() {
			schema, err := a.schema(parameter.Type)
			if err != nil {
				return &ConfigurationError{Component: "query schema", Name: q.Identity() + "." + parameter.Name, Cause: err}
			}
			properties[parameter.Name] = schema
			if parameter.Required && !parameter.HasDefault {
				required = append(required, parameter.Name)
			}
		}
		route := ""
		for _, e := range a.endpoints {
			if e.Identity == q.Identity() {
				route = e.Path
				break
			}
		}
		queryEntries = append(queryEntries, queryCatalogEntry{q.Name, a.catalogNamespace(q.ReadModel.Namespace), route, q.Identity(), q.ReadModel.Identity(), q.DocumentationSummary, map[string]any{"type": "object", "properties": properties, "required": required}})
	}
	slices.SortFunc(commands, func(a, b commandCatalogEntry) int { return strings.Compare(a.Type, b.Type) })
	slices.SortFunc(queryEntries, func(a, b queryCatalogEntry) int { return strings.Compare(a.FullyQualifiedName, b.FullyQualifiedName) })
	body, err = json.Marshal(commands)
	if err != nil {
		return err
	}
	a.catalogJSON["/.cratis/commands"] = body
	body, err = json.Marshal(queryEntries)
	if err != nil {
		return err
	}
	a.catalogJSON["/.cratis/queries"] = body
	return nil
}
func (a *Application) catalogNamespace(namespace string) string {
	if namespace == "" {
		return ""
	}
	parts := strings.Split(namespace, ".")
	if a.options.Routes.SegmentsToSkip >= len(parts) {
		return ""
	}
	return strings.Join(parts[a.options.Routes.SegmentsToSkip:], ".")
}
