// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

//go:embed templates/command.ts.tmpl
var commandTemplate string

type tsCommandProperty struct {
	tsProperty
	WireName, Backing, AccessType string
	Optional                      bool
}
type tsCommand struct {
	Name, Route, Identity, Roles, Response, ResponseConstructor          string
	Command, Validator, Descriptor, Fields, Hook, SetValues, ClearValues string
	Imports, Rules                                                       []string
	Properties                                                           []tsCommandProperty
	Enumerable, Warnings                                                 bool
	Severity                                                             string
}

// renderTypeScriptCommands returns one complete in-memory command/model/barrel
// family. It never publishes files or enables Generate's public TS success mode.
// Response is the finalized graph contract, not the server Handle return type.
func renderTypeScriptCommands(graph *Graph) ([]typescriptOutput, error) {
	if graph == nil || graph.FormatVersion != GraphVersion {
		return nil, fmt.Errorf("unsupported command graph format")
	}
	if err := validateProfile(graph.Profile); err != nil {
		return nil, err
	}
	endpoints, err := metadata.Resolve(graph.Catalog, graph.Profile.routeOptions())
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(endpoints, graph.Endpoints) {
		return nil, fmt.Errorf("command graph endpoints disagree with finalized catalog")
	}
	catalog := map[string]metadata.Command{}
	for _, declaration := range graph.Catalog.Commands {
		catalog[declaration.Type.Identity()] = declaration
	}
	commands := append([]CommandDescriptor(nil), graph.Commands...)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Declaration.Type.Identity() < commands[j].Declaration.Type.Identity()
	})
	nodes, paths := map[string]TypeDescriptor{}, map[string]string{}
	inputs := map[string]bool{}
	seen := map[string]bool{}
	for _, command := range commands {
		identity := command.Declaration.Type.Identity()
		if command.TypeKey == "" || seen[identity] || inputs[command.TypeKey] || !reflect.DeepEqual(command.Declaration, catalog[identity]) {
			return nil, fmt.Errorf("%s: duplicate or unfinalized command declaration", identity)
		}
		seen[identity], inputs[command.TypeKey] = true, true
	}
	if len(seen) != len(catalog) {
		return nil, fmt.Errorf("command graph omits finalized catalog declarations")
	}
	models := *graph
	models.Types = nil
	for _, node := range graph.Types {
		if inputs[node.Key] {
			continue
		}
		if _, exists := nodes[node.Key]; exists {
			return nil, fmt.Errorf("duplicate model key %s", node.Key)
		}
		nodes[node.Key] = node
		file, err := modelPath(node.Name, graph.Profile)
		if err != nil {
			return nil, err
		}
		paths[node.Key] = file
		models.Types = append(models.Types, node)
	}
	views := map[string]tsCommand{}
	exports := map[string]string{}
	reserve := func(file, name, owner string) error {
		key := strings.ToLower(path.Dir(file)) + "/" + name
		if previous, exists := exports[key]; exists {
			return fmt.Errorf("TypeScript barrel export collision: %s and %s", previous, owner)
		}
		exports[key] = owner
		return nil
	}
	for _, node := range models.Types {
		if err := reserve(paths[node.Key], node.Name.Name, node.Key); err != nil {
			return nil, err
		}
		if node.Flags {
			if err := reserve(paths[node.Key], "all"+node.Name.Name, node.Key); err != nil {
				return nil, err
			}
		}
	}
	for _, command := range commands {
		if command.Excluded {
			continue
		}
		file, err := modelPath(command.Declaration.Type, graph.Profile)
		if err != nil {
			return nil, err
		}
		paths[command.TypeKey] = file
		view, err := planCommand(command, nodes, paths, endpoints)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", command.Declaration.Type.Identity(), err)
		}
		for _, name := range []string{view.Name, "I" + view.Name} {
			if err := reserve(file, name, command.TypeKey); err != nil {
				return nil, err
			}
		}
		if len(view.Rules) > 0 {
			if err := reserve(file, view.Name+"Validator", command.TypeKey); err != nil {
				return nil, err
			}
		}
		if _, exists := views[file]; exists {
			return nil, fmt.Errorf("TypeScript command output collision: %s", file)
		}
		views[file] = view
	}
	// Preflight the complete command/model/barrel layout before rendering files.
	barrels := map[string][]string{}
	files := []string{}
	for _, node := range models.Types {
		files = append(files, paths[node.Key])
	}
	for file := range views {
		files = append(files, file)
	}
	for _, file := range files {
		barrels[path.Dir(file)] = append(barrels[path.Dir(file)], strings.TrimSuffix(path.Base(file), ".ts"))
	}
	for directory := range barrels {
		files = append(files, path.Join(directory, "index.ts"))
	}
	if err := validateCommandPaths(files); err != nil {
		return nil, err
	}
	modelOutputs, err := renderTypeScriptModels(&models)
	if err != nil {
		return nil, err
	}
	var outputs []typescriptOutput
	for _, output := range modelOutputs {
		if path.Base(output.path) != "index.ts" {
			outputs = append(outputs, output)
		}
	}
	tmpl, err := template.New("command").Parse(commandTemplate)
	if err != nil {
		return nil, err
	}
	for file, view := range views {
		var buffer bytes.Buffer
		if err := tmpl.Execute(&buffer, view); err != nil {
			return nil, err
		}
		outputs = append(outputs, typescriptOutput{file, buffer.Bytes()})
	}
	for directory, names := range barrels {
		sort.Strings(names)
		var buffer strings.Builder
		buffer.WriteString("// Code generated by arc-gen TypeScript commands; DO NOT EDIT.\n")
		for _, name := range names {
			fmt.Fprintf(&buffer, "export * from './%s';\n", name)
		}
		outputs = append(outputs, typescriptOutput{path.Join(directory, "index.ts"), []byte(buffer.String())})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].path < outputs[j].path })
	return outputs, nil
}

func validateCommandPaths(files []string) error {
	sort.Strings(files)
	used, directories := map[string]string{}, map[string]string{}
	for _, file := range files {
		folded := strings.ToLower(file)
		if previous, exists := used[folded]; exists {
			return fmt.Errorf("TypeScript output collision: %s and %s", previous, file)
		}
		used[folded] = file
		for directory := path.Dir(file); directory != "."; directory = path.Dir(directory) {
			key := strings.ToLower(directory)
			if previous, exists := directories[key]; exists && previous != directory {
				return fmt.Errorf("TypeScript directory case collision: %s and %s", previous, directory)
			}
			directories[key] = directory
		}
	}
	for directory := range directories {
		if _, exists := used[directory]; exists {
			return fmt.Errorf("TypeScript file/directory collision: %s", directory)
		}
	}
	return nil
}

func planCommand(command CommandDescriptor, nodes map[string]TypeDescriptor, paths map[string]string, endpoints []metadata.Endpoint) (tsCommand, error) {
	view := tsCommand{Name: command.Declaration.Type.Name, Identity: tsQuote(command.Declaration.Type.Identity()), ResponseConstructor: "Object"}
	for _, endpoint := range endpoints {
		if endpoint.Identity == command.Declaration.Type.Identity() && endpoint.Method == "POST" && !endpoint.ValidateOnly {
			view.Route = tsQuote(endpoint.Path)
		}
	}
	if view.Route == "" {
		return view, fmt.Errorf("missing finalized command endpoint")
	}
	roles := command.Roles
	if roles == nil {
		roles = []string{}
	}
	data, err := json.Marshal(roles)
	if err != nil {
		return view, err
	}
	view.Roles = string(data)
	if severity := command.Declaration.BlockOnValidationSeverity; severity != nil {
		if *severity < validation.Unknown || *severity > validation.Error {
			return view, fmt.Errorf("unsupported command severity")
		}
		view.Severity = strconv.Itoa(int(*severity))
		view.Warnings = *severity <= validation.Warning
	}
	imports := &tsImports{requests: map[tsImport]bool{}, aliases: map[tsImport]string{}, own: view.Name, reserved: []string{"I" + view.Name, view.Name + "Validator"}}
	base := imports.add("@cratis/arc/commands", "Command", true)
	descriptor := imports.add("@cratis/arc/reflection", "PropertyDescriptor", true)
	fields := imports.add("@cratis/fundamentals", "Fields", true)
	hook := imports.add("@cratis/arc.react/commands", "useCommand", true)
	setValues := imports.add("@cratis/arc.react/commands", "SetCommandValues", false)
	clearValues := imports.add("@cratis/arc.react/commands", "ClearCommandValues", false)
	var validator tsImport
	for _, field := range command.Fields {
		if commandReserved(field.Name) {
			return view, fmt.Errorf("wire field %q collides with command runtime", field.Name)
		}
		if field.HasDefault {
			return view, fmt.Errorf("command defaults require an explicit portable wire-value contract")
		}
		for _, rule := range field.Rules {
			line, err := commandRule(field, rule)
			if err != nil {
				return view, err
			}
			view.Rules = append(view.Rules, line)
		}
	}
	if len(view.Rules) > 0 {
		validator = imports.add("@cratis/arc/commands", "CommandValidator", true)
	}
	node := TypeDescriptor{Key: command.TypeKey, Name: command.Declaration.Type, Kind: "model", Fields: append([]FieldDescriptor(nil), command.Fields...)}
	switch command.ResponseKind {
	case "none":
		if command.Response != nil {
			return view, fmt.Errorf("no-response command contains response metadata")
		}
	case "value":
		if command.Response == nil || command.Response.Nullable && command.Response.Kind != "array" || command.Response.Kind == "record" {
			return view, fmt.Errorf("unsupported or missing finalized response metadata")
		}
		if command.Response.Kind == "array" {
			view.Enumerable = true
		}
		node.Fields = append(node.Fields, FieldDescriptor{Name: "__arcResponse", Type: *command.Response})
	default:
		return view, fmt.Errorf("unsupported finalized response kind %q (no implicit any)", command.ResponseKind)
	}
	model, err := planModelUsingImports(node, nodes, paths, imports, false)
	if err != nil {
		return view, err
	}
	view.Imports = model.Imports
	view.Command, view.Validator, view.Descriptor, view.Fields = imports.aliases[base], imports.aliases[validator], imports.aliases[descriptor], imports.aliases[fields]
	view.Hook, view.SetValues, view.ClearValues = imports.aliases[hook], imports.aliases[setValues], imports.aliases[clearValues]
	if command.Response != nil {
		response := model.Properties[len(model.Properties)-1]
		// Pinned C# uses the element generic even for lists. Retain that annotation
		// mismatch; enumerable constructor metadata still hydrates a runtime list.
		view.Response = strings.TrimSuffix(response.Type, "[]")
		view.ResponseConstructor = response.Constructor
		model.Properties = model.Properties[:len(model.Properties)-1]
	}
	for index, property := range model.Properties {
		field := command.Fields[index]
		optional := field.Optional || field.Type.Nullable
		accessType := property.Type
		if optional {
			accessType += " | undefined"
		}
		view.Properties = append(view.Properties, tsCommandProperty{tsProperty: property, WireName: tsQuote(field.Name), Backing: "_value" + strconv.Itoa(index), AccessType: accessType, Optional: optional})
	}
	return view, nil
}

func commandReserved(name string) bool {
	if strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "constructor", "prototype", "__proto__", "route", "commandName", "validation", "roles", "propertyDescriptors", "requestParameters", "treatWarningsAsErrors", "blockOnValidationSeverity", "hasChanges", "execute", "validate", "validateClientSide", "clear", "revertChanges", "propertyChanged", "onPropertyChanged", "setInitialValues", "setInitialValuesFromCurrentValues", "setMicroservice", "setApiBasePath", "setOrigin", "setHttpHeadersCallback", "buildPayload", "validateRequiredProperties", "filterValidationResultsBySeverity", "buildHeaders", "performRequest", "updateHasChanges":
		return true
	}
	return false
}

func commandRule(field FieldDescriptor, rule validation.RuleDescriptor) (string, error) {
	if strings.Contains(rule.Message, "{PropertyName}") {
		return "", fmt.Errorf("field %q: client message substitution has no portable server equivalent", field.Name)
	}
	if rule.ServerOnly || rule.Optional || rule.Concept || rule.Property != field.Name {
		return "", fmt.Errorf("field %q: rule requires unsupported server-only/optional/concept semantics", field.Name)
	}
	kind := field.Type.Kind
	if kind == "array" {
		kind = "collection"
	}
	if kind != "string" && kind != "number" && kind != "boolean" && kind != "collection" {
		kind = "object"
	}
	data, err := json.Marshal([]validation.RuleDescriptor{rule})
	if err != nil {
		return "", err
	}
	if _, err := validation.ParseRules(string(data), field.Name, kind); err != nil {
		return "", err
	}
	// Bounded rules with paired semantic fixtures. Format and regex projections
	// remain diagnostic until their cross-language corpus is delivered.
	switch rule.Name {
	case "notNull", "notEmpty", "minLength", "maxLength", "length", "greaterThan", "greaterThanOrEqual", "lessThan", "lessThanOrEqual":
	default:
		return "", fmt.Errorf("field %q: unsupported client portable rule %q", field.Name, rule.Name)
	}
	if rule.Name == "notEmpty" && kind != "string" && kind != "collection" {
		return "", fmt.Errorf("field %q: notEmpty requires proved string/collection semantics", field.Name)
	}
	var arguments []string
	for _, argument := range rule.Arguments {
		arguments = append(arguments, string(argument))
	}
	line := "this.ruleFor(c => c[" + tsQuote(field.Name) + "])." + rule.Name + "(" + strings.Join(arguments, ", ") + ").withMessage(" + tsQuote(rule.Message) + ")"
	if rule.Severity != nil {
		line += ".withSeverity(" + strconv.Itoa(int(*rule.Severity)) + ")"
	}
	return line + ";", nil
}
