// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"reflect"

	"github.com/cratis/arc.go/metadata"
)

// index rejects ambiguity instead of merging or renaming declarations. The first
// profile deliberately requires globally distinct simple model/command names;
// namespace-aware domain placement needs shared Graph descriptors, not another
// source analyzer inside this emitter.
func (state *screenplayState) index(graph *Graph) error {
	packages := map[string]bool{}
	for _, pkg := range graph.Packages {
		if pkg.GoPath == "" || packages[pkg.GoPath] {
			return fmt.Errorf("screenplay: empty or duplicate package declaration %q", pkg.GoPath)
		}
		packages[pkg.GoPath] = true
	}
	names := map[string]string{}
	for _, primitive := range []string{"String", "Bool", "Uuid", "Int", "Decimal", "Date", "DateTime", "Enum"} {
		names[primitive] = "Screenplay primitive"
	}
	for _, node := range graph.Types {
		if node.Key == "" || !screenplayName(node.Name.Name, false) {
			return fmt.Errorf("screenplay: invalid type declaration %q (%q)", node.Key, node.Name.Name)
		}
		if _, exists := state.types[node.Key]; exists {
			return fmt.Errorf("screenplay: duplicate type descriptor %q", node.Key)
		}
		if previous, exists := names[node.Name.Name]; exists {
			return fmt.Errorf("screenplay: conflicting type name %q: %s and %s; namespace placement is not inferred", node.Name.Name, previous, node.Key)
		}
		if node.Kind != "model" || node.Import != nil || node.Base != "" || node.Interface != "" || node.DerivedID != "" || len(node.Derivatives) != 0 || node.Discriminator != nil || node.Schemas != nil {
			return fmt.Errorf("screenplay: %s: unsupported %q/opaque/derived type; requires a dedicated Screenplay descriptor", node.Key, node.Kind)
		}
		names[node.Name.Name] = node.Key
		state.types[node.Key] = node
	}
	commands := map[string]metadata.Command{}
	commandNames := map[string]string{}
	for _, command := range graph.Commands {
		identity := command.Declaration.Type.Identity()
		if !screenplayName(command.Declaration.Type.Name, false) {
			return fmt.Errorf("screenplay: invalid command name %q", identity)
		}
		if _, exists := commands[identity]; exists {
			return fmt.Errorf("screenplay: duplicate command declaration %q", identity)
		}
		if previous, exists := commandNames[command.Declaration.Type.Name]; exists {
			return fmt.Errorf("screenplay: conflicting command name %q: %s and %s", command.Declaration.Type.Name, previous, identity)
		}
		node, exists := state.types[command.TypeKey]
		if !exists || node.Name != command.Declaration.Type || !reflect.DeepEqual(node.Fields, command.Fields) {
			return fmt.Errorf("screenplay: %s: missing or conflicting command input type %q", identity, command.TypeKey)
		}
		commands[identity] = command.Declaration
		commandNames[command.Declaration.Type.Name] = identity
	}
	queries := map[string]metadata.Query{}
	for _, query := range graph.Queries {
		identity := query.Declaration.Identity()
		if !screenplayName(query.Declaration.Name, false) {
			return fmt.Errorf("screenplay: invalid query name %q", identity)
		}
		if _, exists := queries[identity]; exists {
			return fmt.Errorf("screenplay: duplicate query declaration %q", identity)
		}
		queries[identity] = query.Declaration
	}

	// A catalog entry without a descriptor is an omitted capability, not an
	// empty successful export. Compare both directions and reject duplicates.
	if len(graph.Catalog.Commands) != len(commands) || len(graph.Catalog.Queries) != len(queries) {
		return fmt.Errorf("screenplay: catalog and descriptors differ; command/query declarations would be omitted")
	}
	for _, command := range graph.Catalog.Commands {
		identity := command.Type.Identity()
		if descriptor, exists := commands[identity]; !exists || !reflect.DeepEqual(descriptor, command) {
			return fmt.Errorf("screenplay: missing, duplicate or conflicting catalog command %q", identity)
		}
		delete(commands, identity)
	}
	for _, query := range graph.Catalog.Queries {
		identity := query.Identity()
		descriptor, exists := queries[identity]
		// Wire analysis enriches the descriptor's identity field after catalog
		// construction. This one enrichment is not a conflicting declaration.
		descriptor.ReadModelIdentityMember = query.ReadModelIdentityMember
		if !exists || !reflect.DeepEqual(descriptor, query) {
			return fmt.Errorf("screenplay: missing, duplicate or conflicting catalog query %q", identity)
		}
		delete(queries, identity)
	}
	return nil
}
