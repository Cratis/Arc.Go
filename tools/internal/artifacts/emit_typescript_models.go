// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/cratis/arc.go/metadata"
)

// This renderer deliberately has no publisher and does not claim command/query
// output. Later emitters can share the validated layout and constructor planning.
type typescriptOutput struct {
	path    string
	content []byte
}

type tsProperty struct {
	Name, Type, Constructor, Marker string
	Enumerable                      bool
}
type tsModel struct {
	Name, Base, DerivedID string
	Imports               []string
	Properties            []tsProperty
	Members               []EnumMember
	Enum                  bool
	AllFlags              string
}

//go:embed templates/model.ts.tmpl
var modelTemplate string

var tsIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
var derivedIdentifier = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func tsName(name string) bool {
	if !tsIdentifier.MatchString(name) {
		return false
	}
	switch name {
	case "String", "Number", "Boolean", "Object", "Date", "Record", "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "do", "else", "enum", "export", "extends", "false", "finally", "for", "function", "if", "import", "in", "instanceof", "new", "null", "return", "super", "switch", "this", "throw", "true", "try", "typeof", "var", "void", "while", "with", "yield", "let", "static", "implements", "interface", "package", "private", "protected", "public", "await", "eval", "arguments", "number", "string", "boolean", "any", "unknown", "never", "object", "undefined":
		return false
	}
	return true
}

func modelPath(name metadata.TypeName, profile ApplicationProfile) (string, error) {
	if !tsName(name.Name) || name.Name == "index" {
		return "", fmt.Errorf("invalid TypeScript export name %q", name.Name)
	}
	namespace, folder := name.Namespace, ""
	longest := ""
	for _, root := range profile.TypeScript.NamespaceRoots {
		if (namespace == root.Namespace || strings.HasPrefix(namespace, root.Namespace+".")) && len(root.Namespace) > len(longest) {
			longest, folder = root.Namespace, root.Folder
		}
	}
	var segments []string
	if longest != "" {
		namespace = strings.TrimPrefix(strings.TrimPrefix(namespace, longest), ".")
		if namespace != "" {
			segments = strings.Split(namespace, ".")
		}
	} else {
		if namespace != "" {
			segments = strings.Split(namespace, ".")
		}
		skip := profile.routeOptions().SegmentsToSkip
		if profile.TypeScript.SegmentsToSkip != nil {
			skip = *profile.TypeScript.SegmentsToSkip
		}
		if skip > len(segments) {
			skip = len(segments)
		}
		segments = segments[skip:]
	}
	for _, segment := range segments {
		if !tsIdentifier.MatchString(segment) {
			return "", fmt.Errorf("unsafe TypeScript namespace segment %q", segment)
		}
	}
	suffix := ".ts"
	if profile.TypeScript.ProxyFileSuffix {
		suffix = ".proxy.ts"
	}
	result := path.Join(append([]string{folder}, append(segments, name.Name+suffix)...)...)
	if !safeRelative(result) {
		return "", fmt.Errorf("unsafe TypeScript model path %q", result)
	}
	return result, nil
}

// renderTypeScriptModels returns a complete, deterministic in-memory model plan,
// or no output on any failure. It accepts only the normalized shared wire graph.
// Filesystem ownership/publication and full proxy families are intentionally absent.
func renderTypeScriptModels(graph *Graph) ([]typescriptOutput, error) {
	if graph == nil || graph.FormatVersion != GraphVersion {
		return nil, fmt.Errorf("unsupported model graph format")
	}
	if err := validateProfile(graph.Profile); err != nil {
		return nil, err
	}
	p := graph.Profile.TypeScript
	if p.SourceGrouping || p.Interfaces || p.Library {
		return nil, fmt.Errorf("model renderer supports per-type classes only")
	}
	nodes := map[string]TypeDescriptor{}
	paths := map[string]string{}
	used := map[string]string{}
	ids := map[string]string{}
	exports := map[string]string{}
	directories := map[string]string{}
	ordered := append([]TypeDescriptor(nil), graph.Types...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })
	for _, node := range ordered {
		if node.Key == "" {
			return nil, fmt.Errorf("model graph requires stable type keys")
		}
		if _, exists := nodes[node.Key]; exists {
			return nil, fmt.Errorf("duplicate model key %s", node.Key)
		}
		if node.Kind != "model" && node.Kind != "enum" {
			return nil, fmt.Errorf("%s: unsupported model kind %q; custom codec/import mappings need executable wire evidence", node.Key, node.Kind)
		}
		if excluded(graph.Profile, node.Name.Identity()) {
			return nil, fmt.Errorf("%s: model graph contains an excluded reachable type", node.Key)
		}
		file, err := modelPath(node.Name, graph.Profile)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", node.Key, err)
		}
		folded := strings.ToLower(file)
		if previous, exists := used[folded]; exists {
			return nil, fmt.Errorf("TypeScript output collision: %s and %s", previous, node.Key)
		}
		used[folded] = node.Key
		for directory := path.Dir(file); directory != "."; directory = path.Dir(directory) {
			foldedDirectory := strings.ToLower(directory)
			if previous, exists := directories[foldedDirectory]; exists && previous != directory {
				return nil, fmt.Errorf("TypeScript directory case collision: %s and %s", previous, directory)
			}
			directories[foldedDirectory] = directory
		}
		names := []string{node.Name.Name}
		if node.Kind == "enum" && node.Flags {
			names = append(names, "all"+node.Name.Name)
		}
		for _, name := range names {
			export := strings.ToLower(path.Dir(file)) + "/" + name
			if previous, exists := exports[export]; exists {
				return nil, fmt.Errorf("TypeScript barrel export collision: %s and %s", previous, node.Key)
			}
			exports[export] = node.Key
		}
		if node.DerivedID != "" {
			id := strings.ToLower(node.DerivedID)
			if !derivedIdentifier.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
				return nil, fmt.Errorf("%s: invalid derived UUID", node.Key)
			}
			if previous, exists := ids[id]; exists {
				return nil, fmt.Errorf("duplicate derived UUID: %s and %s", previous, node.Key)
			}
			ids[id] = node.Key
			if node.Base == "" || node.Interface == "" {
				return nil, fmt.Errorf("%s: polymorphic model requires an explicit concrete base and interface", node.Key)
			}
		}
		if len(node.Derivatives) != 0 {
			return nil, fmt.Errorf("%s: interface derivative lists are not supported by the model renderer", node.Key)
		}
		paths[node.Key], nodes[node.Key] = file, node
	}
	if err := rejectConstructorCycles(ordered); err != nil {
		return nil, err
	}
	tmpl, err := template.New("model").Parse(modelTemplate)
	if err != nil {
		return nil, err
	}
	var outputs []typescriptOutput
	barrels := map[string][]string{}
	for _, node := range ordered {
		view, err := planModel(node, nodes, paths)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", node.Key, err)
		}
		var buffer bytes.Buffer
		if err := tmpl.Execute(&buffer, view); err != nil {
			return nil, err
		}
		outputs = append(outputs, typescriptOutput{paths[node.Key], buffer.Bytes()})
		directory := path.Dir(paths[node.Key])
		barrels[directory] = append(barrels[directory], strings.TrimSuffix(path.Base(paths[node.Key]), ".ts"))
	}
	for directory, names := range barrels {
		sort.Strings(names)
		var buffer strings.Builder
		buffer.WriteString("// Code generated by arc-gen TypeScript models; DO NOT EDIT.\n")
		for _, name := range names {
			fmt.Fprintf(&buffer, "export * from './%s';\n", name)
		}
		outputs = append(outputs, typescriptOutput{path.Join(directory, "index.ts"), []byte(buffer.String())})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].path < outputs[j].path })
	// Barrels participate in the same file/case/directory preflight as models.
	allPaths := map[string]string{}
	for _, output := range outputs {
		folded := strings.ToLower(output.path)
		if previous, exists := allPaths[folded]; exists {
			return nil, fmt.Errorf("TypeScript output collision: %s and %s", previous, output.path)
		}
		allPaths[folded] = output.path
	}
	for _, output := range outputs {
		for directory := path.Dir(output.path); directory != "."; directory = path.Dir(directory) {
			if _, exists := allPaths[strings.ToLower(directory)]; exists {
				return nil, fmt.Errorf("TypeScript file/directory collision: %s", directory)
			}
		}
	}
	return outputs, nil
}

type tsImport struct {
	module, name string
}
type tsImports struct {
	requests map[tsImport]bool
	aliases  map[tsImport]string
	own      string
}

func (i *tsImports) add(module, name string, value bool) tsImport {
	key := tsImport{module: module, name: name}
	i.requests[key] = i.requests[key] || value
	return key
}
func (i *tsImports) resolve() []string {
	keys := make([]tsImport, 0, len(i.requests))
	for key := range i.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].module != keys[b].module {
			return keys[a].module < keys[b].module
		}
		return keys[a].name < keys[b].name
	})
	used := map[string]bool{i.own: true, "String": true, "Number": true, "Boolean": true, "Object": true, "Date": true, "Record": true}
	var lines []string
	for _, key := range keys {
		alias := key.name
		for suffix := 2; used[alias]; suffix++ {
			alias = key.name + "_" + strconv.Itoa(suffix)
		}
		used[alias], i.aliases[key] = true, alias
		binding := key.name
		if alias != key.name {
			binding += " as " + alias
		}
		kind := ""
		if !i.requests[key] {
			kind = "type "
		}
		lines = append(lines, "import "+kind+"{ "+binding+" } from "+tsQuote(key.module)+";")
	}
	return lines
}
func tsQuote(value string) string { data, _ := json.Marshal(value); return string(data) }

func relativeModelImport(from, to string) (string, error) {
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(strings.TrimSuffix(to, ".ts")))
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel, nil
}

func planModel(node TypeDescriptor, nodes map[string]TypeDescriptor, paths map[string]string) (tsModel, error) {
	view := tsModel{Name: node.Name.Name, Enum: node.Kind == "enum", DerivedID: strings.ToLower(node.DerivedID)}
	imports := &tsImports{requests: map[tsImport]bool{}, aliases: map[tsImport]string{}, own: node.Name.Name}
	reference := func(key string, value bool) (tsImport, error) {
		target, exists := nodes[key]
		if !exists {
			return tsImport{}, fmt.Errorf("missing referenced model %q", key)
		}
		module, err := relativeModelImport(paths[node.Key], paths[key])
		if err != nil {
			return tsImport{}, err
		}
		return imports.add(module, target.Name.Name, value), nil
	}
	if view.Enum {
		if len(node.Fields) != 0 || node.Base != "" || node.DerivedID != "" || node.Interface != "" || len(node.Members) == 0 {
			return view, fmt.Errorf("invalid enum descriptor")
		}
		seen := map[string]bool{}
		var flags []string
		for _, member := range node.Members {
			value, err := strconv.ParseInt(member.Value, 10, 64)
			if err != nil || value < -9007199254740991 || value > 9007199254740991 || node.Flags && (value < -2147483648 || value > 2147483647) {
				return view, fmt.Errorf("enum value exceeds safe number/bitwise limits")
			}
			if !tsIdentifier.MatchString(member.Name) || member.Name == "__proto__" || member.Name == "constructor" || member.Name == "prototype" || seen[member.Name] {
				return view, fmt.Errorf("invalid or duplicate enum member %q", member.Name)
			}
			seen[member.Name] = true
			name := member.Name
			access := node.Name.Name + "." + name
			if !tsName(name) {
				name = tsQuote(name)
				access = node.Name.Name + "[" + name + "]"
			}
			view.Members = append(view.Members, EnumMember{Name: name, Value: strconv.FormatInt(value, 10)})
			if value != 0 {
				flags = append(flags, access)
			}
		}
		if node.Flags {
			view.AllFlags = strings.Join(flags, " | ")
			if view.AllFlags == "" {
				view.AllFlags = "0"
			}
		}
		return view, nil
	}
	if len(node.Members) != 0 || node.Flags {
		return view, fmt.Errorf("invalid model descriptor")
	}
	fields := node.Fields
	var baseImport tsImport
	if node.Base != "" {
		base, exists := nodes[node.Base]
		if !exists || base.Kind != "model" || node.Interface == "" || base.Interface != node.Interface || node.DerivedID == "" {
			return view, fmt.Errorf("derived model requires a matching declared base contract")
		}
		var err error
		baseImport, err = reference(node.Base, true)
		if err != nil {
			return view, err
		}
		inherited := map[string]FieldDescriptor{}
		for _, field := range base.Fields {
			inherited[field.Name] = field
		}
		fields = nil
		for _, field := range node.Fields {
			if parent, exists := inherited[field.Name]; exists {
				if !reflect.DeepEqual(parent, field) {
					return view, fmt.Errorf("derived model changes base field %q", field.Name)
				}
				delete(inherited, field.Name)
			} else {
				fields = append(fields, field)
			}
		}
		if len(inherited) != 0 {
			return view, fmt.Errorf("derived model omits base fields")
		}
	}
	var fieldImport, derivedImport tsImport
	if len(fields) != 0 {
		fieldImport = imports.add("@cratis/fundamentals", "field", true)
	}
	if node.DerivedID != "" {
		derivedImport = imports.add("@cratis/fundamentals", "derivedType", true)
	}
	type planned struct {
		property  tsProperty
		typ, ctor tsImport
	}
	var properties []planned
	seen := map[string]bool{}
	for _, field := range fields {
		if field.Name == "" || field.Name == "constructor" || field.Name == "prototype" || field.Name == "__proto__" || seen[field.Name] {
			return view, fmt.Errorf("invalid or duplicate wire field %q", field.Name)
		}
		seen[field.Name] = true
		wire := field.Type
		property := tsProperty{Name: tsQuote(field.Name), Marker: "!"}
		if tsName(field.Name) {
			property.Name = field.Name
		}
		if field.Optional || wire.Nullable {
			property.Marker = "?"
		}
		if wire.Kind == "array" {
			if wire.Element == nil || wire.Element.Nullable || wire.Element.Kind == "array" || wire.Element.Kind == "record" {
				return view, fmt.Errorf("field %q: nested/nullable collection elements lack supported hydration metadata", field.Name)
			}
			property.Enumerable = true
			wire = *wire.Element
		}
		item := planned{property: property}
		switch wire.Kind {
		case "string":
			item.property.Type, item.property.Constructor = "string", "String"
		case "boolean":
			item.property.Type, item.property.Constructor = "boolean", "Boolean"
		case "number":
			item.property.Type, item.property.Constructor = "number", "Number"
		case "Date":
			item.property.Type, item.property.Constructor = "Date", "Date"
		case "Guid", "DateOnly", "TimeOnly", "TimeSpan":
			item.typ = imports.add("@cratis/fundamentals", wire.Kind, true)
			item.ctor = item.typ
		case "model", "enum":
			target, exists := nodes[wire.Target]
			if !exists || target.Kind != wire.Kind {
				return view, fmt.Errorf("field %q: missing or mismatched target %q", field.Name, wire.Target)
			}
			var err error
			item.typ, err = reference(wire.Target, wire.Kind == "model")
			if err != nil {
				return view, err
			}
			if wire.Kind == "enum" {
				item.property.Constructor = "Number"
			} else {
				item.ctor = item.typ
			}
		case "record":
			if wire.Element == nil || wire.Element.Nullable {
				return view, fmt.Errorf("field %q: unsupported dictionary values", field.Name)
			}
			element := wire.Element.Kind
			if element != "string" && element != "number" && element != "boolean" {
				return view, fmt.Errorf("field %q: dictionary requires primitive values", field.Name)
			}
			item.property.Type, item.property.Constructor = "Record<string, "+element+">", "Object"
		default:
			return view, fmt.Errorf("field %q: unsupported wire kind %q (no implicit any)", field.Name, wire.Kind)
		}
		properties = append(properties, item)
	}
	view.Imports = imports.resolve()
	if node.Base != "" {
		view.Base = imports.aliases[baseImport]
	}
	for _, item := range properties {
		if item.typ.name != "" {
			item.property.Type = imports.aliases[item.typ]
		}
		if item.ctor.name != "" {
			item.property.Constructor = imports.aliases[item.ctor]
		}
		// Decorator names are planned too: local exports may be named field/derivedType.
		item.property.Constructor = imports.aliases[fieldImport] + "(" + item.property.Constructor
		if item.property.Enumerable {
			item.property.Constructor += ", true"
			item.property.Type += "[]"
		}
		item.property.Constructor += ")"
		view.Properties = append(view.Properties, item.property)
	}
	if node.DerivedID != "" {
		view.DerivedID = imports.aliases[derivedImport] + "(" + tsQuote(view.DerivedID) + ")"
	}
	return view, nil
}
