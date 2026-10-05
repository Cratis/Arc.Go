// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// screenplayMetadata is a partial, descriptive .play document, never an
// executable application model. Diagnostics also appear as document comments so
// copying the bytes cannot silently discard the export's limits.
//
// The grammar is Cratis.Screenplay 4.48.1 (e5b5698f9dbcf61c39e4cc3f43fcf092c9e0b5fc),
// the version consumed by Arc 7c1e780. See testdata/screenplay/contract.md.
// arc-gen publishes it as a file through -screenplay-out; there is no HTTP
// route or embedded viewer.
type screenplayMetadata struct {
	Document    []byte
	Diagnostics []string
}

// exportScreenplayMetadata reads, but never retains or changes, graph. It owns
// its returned bytes and diagnostics. Callers must not mutate graph concurrently.
// Unsupported shapes and conflicting declarations fail without document bytes;
// missing behavioral analysis is explicitly reported, not guessed from HTTP or
// TypeScript metadata. There is no source loading, file access or publication.
func exportScreenplayMetadata(graph *Graph) (screenplayMetadata, error) {
	if graph == nil {
		return screenplayMetadata{}, fmt.Errorf("screenplay: graph is nil")
	}
	if graph.FormatVersion != GraphVersion && graph.FormatVersion != ContractGraphVersion {
		return screenplayMetadata{}, fmt.Errorf("screenplay: unsupported graph version %d", graph.FormatVersion)
	}
	if len(graph.Diagnostics) != 0 {
		return screenplayMetadata{}, fmt.Errorf("screenplay: resolve graph diagnostics before export")
	}
	if len(graph.Commands)+len(graph.Queries) == 0 {
		return screenplayMetadata{}, fmt.Errorf("screenplay: no command or query descriptors to export")
	}
	if !screenplayName(graph.Profile.Name, false) {
		return screenplayMetadata{}, fmt.Errorf("screenplay: domain %q must be an ASCII identifier; names are not silently rewritten", graph.Profile.Name)
	}

	state := screenplayState{types: map[string]TypeDescriptor{}, diagnostics: map[string]bool{}}
	state.note("SPG001: partial metadata only; Graph has no event inventory (identity, generation, classification), command event production, projection/reducer mappings, reactions, constraints, specifications or screens; absence is unknown, not proof that these capabilities are absent")
	state.note("SPG002: Application/Metadata and per-artifact slices are presentation grouping only; Graph has no domain module, feature or slice descriptors")
	state.note("SPG003: handler implementations, decision reads, concurrency, validation and effective authorization policies are not modeled; this document is not an executable or security contract")
	state.note("SPG004: HTTP routes, binding, codecs, numeric bounds and missing/null/zero distinctions are not a Screenplay wire contract; property names are the Graph wire names")
	if err := state.index(graph); err != nil {
		return screenplayMetadata{}, err
	}

	var body strings.Builder
	fmt.Fprintf(&body, "domain %s\n", graph.Profile.Name)
	nodes := slices.Clone(graph.Types)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name.Name < nodes[j].Name.Name })
	for _, node := range nodes {
		// Screenplay 4.48.1 rejects bare type declarations (TypeWithoutProperties).
		// Do not invent a property for an empty model or a no-input command.
		if len(node.Fields) == 0 {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s: model requires at least one property in Screenplay 4.48.1", node.Key)
		}
		fmt.Fprintf(&body, "\ntype %s\n", node.Name.Name)
		if err := state.fields(&body, node.Fields, "    ", node.Key, false); err != nil {
			return screenplayMetadata{}, err
		}
	}

	body.WriteString("\nmodule Application\n    feature Metadata\n")
	commands := slices.Clone(graph.Commands)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Declaration.Type.Identity() < commands[j].Declaration.Type.Identity()
	})
	for _, command := range commands {
		identity := command.Declaration.Type.Identity()
		if command.Excluded {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s: excluded command has no complete input descriptor", identity)
		}
		if command.Input == nil || command.Input.Kind != "model" || command.Input.Target != command.TypeKey {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s: command requires a normalized model input descriptor", identity)
		}
		if command.ResponseKind != "none" {
			state.note("SPG100: command %q response kind %q is not event-production evidence; response and behavior omitted", identity, command.ResponseKind)
		}
		if command.Declaration.Authorization != nil || len(command.Roles) != 0 {
			state.note("SPG101: command %q authorization declaration omitted; no policy implementation descriptor", identity)
		}
		name := command.Declaration.Type.Name
		fmt.Fprintf(&body, "\n        slice StateChange Command%s\n            command %s\n", name, name)
		if err := state.fields(&body, command.Fields, "                ", identity, false); err != nil {
			return screenplayMetadata{}, err
		}
	}

	queries := slices.Clone(graph.Queries)
	// A query identity includes its name after the read-model namespace. Sorting
	// that whole string can interleave A.Listing.A, A.Listing.Other.All and
	// A.Listing.Z. Order model groups first, then queries within each group.
	sort.Slice(queries, func(i, j int) bool {
		left, right := queries[i], queries[j]
		if left.Declaration.ReadModel != right.Declaration.ReadModel {
			return left.Declaration.ReadModel.Identity() < right.Declaration.ReadModel.Identity()
		}
		if left.TypeKey != right.TypeKey {
			return left.TypeKey < right.TypeKey
		}
		return left.Declaration.Identity() < right.Declaration.Identity()
	})
	lastModel := ""
	for _, query := range queries {
		identity := query.Declaration.Identity()
		if query.Excluded {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s: excluded query has no complete result descriptor", identity)
		}
		if query.Delivery != "snapshot" && query.Delivery != "observable" || query.Declaration.Observable != (query.Delivery == "observable") {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s: unsupported or conflicting query delivery %q", identity, query.Delivery)
		}
		result := query.Result
		if result.Kind == "array" && result.Element != nil {
			result = *result.Element
		}
		if result.Kind != "model" || result.Target != query.TypeKey {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s: query result must reference its declared read model", identity)
		}
		node, exists := state.types[query.TypeKey]
		if !exists || node.Name != query.Declaration.ReadModel {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s: missing or conflicting read-model descriptor %q", identity, query.TypeKey)
		}
		if lastModel != query.TypeKey {
			fmt.Fprintf(&body, "\n        slice StateView ReadModel%s\n            readmodel %s\n", node.Name.Name, node.Name.Name)
			if err := state.fields(&body, node.Fields, "                ", node.Key, false); err != nil {
				return screenplayMetadata{}, err
			}
			lastModel = query.TypeKey
		}
		returnType, err := state.typeRef(query.Result, false, 0)
		if err != nil {
			return screenplayMetadata{}, fmt.Errorf("screenplay: %s result: %w", identity, err)
		}
		if query.Delivery == "observable" {
			returnType = "observable " + returnType
		}
		fmt.Fprintf(&body, "\n            query %s => %s\n", query.Declaration.Name, returnType)
		if err := state.fields(&body, query.Parameters, "                filter ", identity, true); err != nil {
			return screenplayMetadata{}, err
		}
		if len(query.Parameters) != 0 {
			state.note("SPG102: query %q arguments exported as filters; Graph does not declare a Screenplay 'by' key", identity)
		}
		if query.Paged || len(query.SortFields) != 0 {
			state.note("SPG103: query %q paging and sorting are serving concerns omitted from the document", identity)
		}
		if query.Declaration.Authorization != nil || query.Declaration.ReadModelAuthorization != nil || len(query.Roles) != 0 {
			state.note("SPG101: query %q authorization declaration omitted; no policy implementation descriptor", identity)
		}
	}

	diagnostics := make([]string, 0, len(state.diagnostics))
	for message := range state.diagnostics {
		diagnostics = append(diagnostics, message)
	}
	sort.Strings(diagnostics)
	var document strings.Builder
	document.WriteString("// Arc.Go partial metadata export; Screenplay 4.48.1. Not an executable application model.\n")
	for _, message := range diagnostics {
		fmt.Fprintf(&document, "// %s\n", message)
	}
	document.WriteByte('\n')
	document.WriteString(body.String())
	return screenplayMetadata{Document: []byte(document.String()), Diagnostics: diagnostics}, nil
}

type screenplayState struct {
	types       map[string]TypeDescriptor
	diagnostics map[string]bool
}

func (state *screenplayState) note(format string, args ...any) {
	state.diagnostics[fmt.Sprintf(format, args...)] = true
}

func (state *screenplayState) fields(out *strings.Builder, fields []FieldDescriptor, indent, owner string, parameter bool) error {
	ordered := slices.Clone(fields)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	previous := ""
	for _, field := range ordered {
		if !screenplayName(field.Name, true) || field.Name == previous {
			return fmt.Errorf("screenplay: %s: invalid or duplicate property %q", owner, field.Name)
		}
		previous = field.Name
		typeName, err := state.typeRef(field.Type, field.Optional, 0)
		if err != nil {
			return fmt.Errorf("screenplay: %s.%s: %w", owner, field.Name, err)
		}
		name := field.Name
		// PropertyLineParser accepts @ for all property names, not query parameters.
		// Escaping all properties also avoids directive collisions across bodies.
		if !parameter {
			name = "@" + name
		}
		fmt.Fprintf(out, "%s%s %s\n", indent, name, typeName)
		if len(field.Rules) != 0 || field.HasDefault || field.Required {
			state.note("SPG104: %q property %q validation/default/required binding semantics omitted", owner, field.Name)
		}
	}
	return nil
}

func (state *screenplayState) typeRef(value WireType, optional bool, depth int) (string, error) {
	if depth > 2 {
		return "", fmt.Errorf("unsupported nested wire type; Screenplay has one collection and optional suffix")
	}
	if value.Contract != nil && (value.Contract.Concept != "" || value.Contract.Schemas != nil || value.Contract.ByteArray) {
		return "", fmt.Errorf("concept, custom schema or byte codec requires a dedicated Screenplay descriptor")
	}
	optional = optional || value.Nullable
	var name string
	switch value.Kind {
	case "string":
		name = "String"
	case "boolean":
		name = "Bool"
	case "Guid":
		name = "Uuid"
	case "Date":
		if value.Contract == nil || value.Contract.Declared != "time.Time" {
			return "", fmt.Errorf("date type requires the original temporal descriptor; Date and DateTime cannot be inferred from a TypeScript hint")
		}
		name = "DateTime"
	case "number":
		if value.Contract == nil || value.Contract.Scalar == nil {
			return "", fmt.Errorf("number requires Scalar.GoKind; Int and Decimal cannot be inferred from a TypeScript hint")
		}
		switch value.Contract.Scalar.GoKind {
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
			name = "Int"
		case "float32", "float64":
			name = "Decimal"
		default:
			return "", fmt.Errorf("unsupported numeric Go kind %q", value.Contract.Scalar.GoKind)
		}
	case "model":
		node, exists := state.types[value.Target]
		if !exists {
			return "", fmt.Errorf("missing model descriptor %q", value.Target)
		}
		name = node.Name.Name
	case "optional", "array":
		if value.Element == nil {
			return "", fmt.Errorf("%s requires an element descriptor", value.Kind)
		}
		var err error
		name, err = state.typeRef(*value.Element, false, depth+1)
		if err != nil {
			return "", err
		}
		if value.Kind == "array" {
			if strings.ContainsAny(name, "[]?") {
				return "", fmt.Errorf("nested collections or nullable elements have no Screenplay 4.48.1 type reference")
			}
			name += "[]"
		} else {
			optional = true
		}
	default:
		return "", fmt.Errorf("unsupported wire kind %q; a dedicated Screenplay descriptor is required", value.Kind)
	}
	if optional && !strings.HasSuffix(name, "?") {
		name += "?"
	}
	return name, nil
}

func screenplayName(name string, property bool) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if i == 0 {
			if c != '_' && (!letter || property && c >= 'A' && c <= 'Z') {
				return false
			}
		} else if !letter && c != '_' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
