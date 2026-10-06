// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	_ "embed"
	"fmt"
	"math"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/validation"
)

//go:embed templates/query.ts.tmpl
var queryTemplate string

type tsQueryParameter struct {
	tsProperty
	WireName, Optional string
}
type tsQuerySort struct{ Name, WireName, Backing string }
type tsQuery struct {
	Name, Route, Identity, Roles, Data, Constructor, Base, Descriptor, HTTP string
	ParametersName, Required, Actions, QueryActions, Item                   string
	Validator                                                               string
	Imports, Hooks, Rules, EmptyAsMissing                                   []string
	Parameters                                                              []tsQueryParameter
	SortFields                                                              []tsQuerySort
	Enumerable, Observable                                                  bool
}

// renderTypeScriptQueries coordinates a complete in-memory query/model/command
// family. It delegates transport and hooks to the locked Arc runtime, and never
// publishes outputs or infers a provider's paging/sorting contract.
func renderTypeScriptQueries(graph *Graph) ([]typescriptOutput, error) {
	if graph == nil || graph.FormatVersion != GraphVersion && graph.FormatVersion != ContractGraphVersion {
		return nil, fmt.Errorf("unsupported query graph format")
	}
	if err := validateProfile(graph.Profile); err != nil {
		return nil, err
	}
	base, err := renderTypeScriptCommands(graph)
	if err != nil {
		return nil, err
	}
	nodes, paths := map[string]TypeDescriptor{}, map[string]string{}
	inputs := map[string]bool{}
	for _, command := range graph.Commands {
		inputs[command.TypeKey] = true
	}
	for _, node := range graph.Types {
		if node.TSIncluded != nil && !*node.TSIncluded {
			continue
		}
		if inputs[node.Key] {
			continue
		}
		nodes[node.Key] = node
		paths[node.Key], err = modelPath(node.Name, graph.Profile)
		if err != nil {
			return nil, err
		}
	}
	catalog := map[string]metadata.Query{}
	for _, declaration := range graph.Catalog.Queries {
		catalog[declaration.Identity()] = declaration
	}
	queries := append([]QueryDescriptor(nil), graph.Queries...)
	sort.Slice(queries, func(i, j int) bool { return queries[i].Declaration.Identity() < queries[j].Declaration.Identity() })
	seen, exports := map[string]bool{}, map[string]string{}
	reserve := func(file, name, owner string) error {
		key := strings.ToLower(path.Dir(file)) + "/" + name
		if previous, exists := exports[key]; exists {
			return fmt.Errorf("TypeScript barrel export collision: %s and %s", previous, owner)
		}
		exports[key] = owner
		return nil
	}
	for _, node := range nodes {
		if err := reserve(paths[node.Key], node.Name.Name, node.Key); err != nil {
			return nil, err
		}
		if node.Flags {
			if err := reserve(paths[node.Key], "all"+node.Name.Name, node.Key); err != nil {
				return nil, err
			}
		}
	}
	for _, command := range graph.Commands {
		if command.Excluded {
			continue
		}
		file, err := modelPath(command.Declaration.Type, graph.Profile)
		if err != nil {
			return nil, err
		}
		names := []string{command.Declaration.Type.Name, "I" + command.Declaration.Type.Name}
		for _, field := range command.Fields {
			if len(field.Rules) > 0 {
				names = append(names, command.Declaration.Type.Name+"Validator")
				break
			}
		}
		for _, name := range names {
			if err := reserve(file, name, command.TypeKey); err != nil {
				return nil, err
			}
		}
	}
	outputs := []typescriptOutput{}
	for _, output := range base {
		if path.Base(output.path) != "index.ts" {
			outputs = append(outputs, output)
		}
	}
	tmpl, err := template.New("query").Parse(queryTemplate)
	if err != nil {
		return nil, err
	}
	for _, query := range queries {
		identity := query.Declaration.Identity()
		declaration := query.Declaration
		// Wire analysis attaches the declared identity field after route resolution.
		// It affects hydration, not endpoint identity. Validate it below separately.
		declaration.ReadModelIdentityMember = catalog[identity].ReadModelIdentityMember
		if seen[identity] || !reflect.DeepEqual(declaration, catalog[identity]) {
			return nil, fmt.Errorf("%s: duplicate or unfinalized query declaration", identity)
		}
		seen[identity] = true
		if query.Excluded {
			continue
		}
		file, err := modelPath(metadata.TypeName{Namespace: query.Declaration.ReadModel.Namespace, Name: query.Declaration.Name}, graph.Profile)
		if err != nil {
			return nil, err
		}
		view, err := planQuery(query, file, nodes, paths, graph.Endpoints)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", identity, err)
		}
		names := []string{view.Name, view.ParametersName}
		if len(view.Rules) > 0 {
			names = append(names, view.Name+"Validator")
		}
		for _, name := range names {
			if name != "" {
				if err := reserve(file, name, identity); err != nil {
					return nil, err
				}
			}
		}
		var buffer bytes.Buffer
		if err := tmpl.Execute(&buffer, view); err != nil {
			return nil, err
		}
		outputs = append(outputs, typescriptOutput{file, buffer.Bytes()})
	}
	if len(seen) != len(catalog) {
		return nil, fmt.Errorf("query graph omits finalized catalog declarations")
	}
	barrels := map[string][]string{}
	files := []string{}
	for _, output := range outputs {
		files = append(files, output.path)
		barrels[path.Dir(output.path)] = append(barrels[path.Dir(output.path)], strings.TrimSuffix(path.Base(output.path), ".ts"))
	}
	for directory, names := range barrels {
		sort.Strings(names)
		var buffer strings.Builder
		buffer.WriteString("// Code generated by arc-gen TypeScript queries; DO NOT EDIT.\n")
		for _, name := range names {
			fmt.Fprintf(&buffer, "export * from './%s';\n", name)
		}
		file := path.Join(directory, "index.ts")
		files = append(files, file)
		outputs = append(outputs, typescriptOutput{file, []byte(buffer.String())})
	}
	if err := validateCommandPaths(files); err != nil {
		return nil, err
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].path < outputs[j].path })
	return outputs, nil
}

func planQuery(query QueryDescriptor, file string, nodes map[string]TypeDescriptor, paths map[string]string, endpoints []metadata.Endpoint) (tsQuery, error) {
	view := tsQuery{Name: query.Declaration.Name, Identity: tsQuote(query.Declaration.Identity()), Roles: tsQuoteList(query.Roles)}
	if query.Delivery != "snapshot" && query.Delivery != "observable" {
		return view, fmt.Errorf("unsupported query delivery %q", query.Delivery)
	}
	view.Observable = query.Delivery == "observable"
	if view.Observable != query.Declaration.Observable {
		return view, fmt.Errorf("query delivery disagrees with finalized observable declaration")
	}
	if view.Observable {
		if query.PortableRules {
			return view, fmt.Errorf("observable query validation rules require paired client semantics")
		}
		for _, field := range query.Parameters {
			if len(field.Rules) > 0 {
				return view, fmt.Errorf("observable query validation rules require paired client semantics")
			}
		}
	}
	result := query.Result
	if result.Kind == "array" {
		if result.Element == nil || result.Element.Nullable {
			return view, fmt.Errorf("unsupported %s collection result", query.Delivery)
		}
		view.Enumerable = true
		result = *result.Element
	}
	model, exists := nodes[result.Target]
	if result.Kind != "model" || !exists || model.Kind != "model" || result.Target != query.TypeKey || model.Name != query.Declaration.ReadModel {
		return view, fmt.Errorf("snapshot result must match finalized model-owned wire data (no opaque provider or any)")
	}
	if view.Observable && view.Enumerable {
		if identity := query.Declaration.ReadModelIdentityMember; identity != "" && identity != "id" {
			return view, fmt.Errorf("observable collection identity metadata must select id; other identity layouts are unsupported")
		}
		if err := validateObservableIdentity(model); err != nil {
			return view, err
		}
	}
	if query.Paged && !view.Enumerable {
		return view, fmt.Errorf("paged snapshot must have enumerable wire data")
	}
	if identity := query.Declaration.ReadModelIdentityMember; identity != "" {
		found := false
		for _, field := range model.Fields {
			found = found || field.Name == identity && field.Identity
		}
		if !found {
			return view, fmt.Errorf("unfinalized read-model identity field")
		}
	}
	methods := map[string]string{}
	for _, endpoint := range endpoints {
		if endpoint.Identity == query.Declaration.Identity() {
			methods[endpoint.Method] = endpoint.Path
		}
	}
	preference := query.ClientHTTP
	if preference == "" && methods["GET"] == "" && methods["QUERY"] != "" {
		preference = "Query"
	}
	if preference != "" && preference != "Get" && preference != "Query" && preference != "Auto" {
		return view, fmt.Errorf("invalid client HTTP preference %q", preference)
	}
	if (preference == "Get" || preference == "Auto") && methods["GET"] == "" || preference == "Query" && methods["QUERY"] == "" {
		return view, fmt.Errorf("client HTTP preference is incompatible with finalized endpoints")
	}
	route := methods["GET"]
	if route == "" {
		route = methods["QUERY"]
	}
	if route == "" || methods["GET"] != "" && methods["QUERY"] != "" && methods["GET"] != methods["QUERY"] {
		return view, fmt.Errorf("missing or inconsistent finalized query endpoint")
	}
	view.Route = tsQuote(route)
	imports := &tsImports{requests: map[tsImport]bool{}, aliases: map[tsImport]string{}, own: view.Name, reserved: []string{view.Name + "Parameters", view.Name + "Validator", view.Name + "SortBy", view.Name + "SortByWithoutQuery"}}
	baseName := "QueryFor"
	if view.Observable {
		baseName = "ObservableQueryFor"
	}
	base := imports.add("@cratis/arc/queries", baseName, true)
	descriptor := imports.add("@cratis/arc/reflection", "ParameterDescriptor", true)
	var http, actions, queryActions, validator tsImport
	if preference != "" {
		http = imports.add("@cratis/arc/queries", "QueryHttpMethod", true)
	}
	declared := map[string]FieldDescriptor{}
	for _, field := range model.Fields {
		declared[field.Name] = field
	}
	sortFields := append([]string(nil), query.SortFields...)
	sort.Strings(sortFields)
	for index, name := range sortFields {
		field, ok := declared[name]
		if !ok || !field.Sortable || index > 0 && name == sortFields[index-1] {
			return view, fmt.Errorf("sorting requires unique declared sortable result-wire fields: %q", name)
		}
		switch field.Type.Kind {
		case "string", "number", "boolean", "enum":
			if field.Type.Nullable {
				return view, fmt.Errorf("unsupported nullable sortable result field %q", name)
			}
		default:
			return view, fmt.Errorf("unsupported sortable result field %q", name)
		}
		property := name
		if !tsName(name) {
			property = tsQuote(name)
		}
		if strings.HasPrefix(name, "_") || name == "constructor" || name == "prototype" || name == "query" {
			return view, fmt.Errorf("sorting helper member collision %q", name)
		}
		if view.Enumerable {
			view.SortFields = append(view.SortFields, tsQuerySort{property, tsQuote(name), "_field" + strconv.Itoa(index)})
		}
	}
	if len(view.SortFields) > 0 {
		actions = imports.add("@cratis/arc/queries", "SortingActions", true)
		actionsName := "SortingActionsForQuery"
		if view.Observable {
			actionsName = "SortingActionsForObservableQuery"
		}
		queryActions = imports.add("@cratis/arc/queries", actionsName, true)
	}
	// Hook imports must be registered before the shared type/constructor planner
	// resolves aliases, including collisions with locally named result models.
	hookImports := queryHookImports(view, imports)
	required := []string{}
	for _, field := range query.Parameters {
		// Arc 22.48.2 UrlHelpers interpolates keys into a RegExp without
		// escaping. Even literal snapshot routes pass through that helper.
		if strings.ContainsAny(field.Name, `\.^$*+?()[]{}|`) {
			return view, fmt.Errorf("query parameter %q is incompatible with Arc 22.48.2 route parameter helper", field.Name)
		}
		if queryReserved(field.Name) || view.Observable && observableQueryReserved(field.Name) {
			return view, fmt.Errorf("parameter %q collides with query runtime", field.Name)
		}
		if len(field.Rules) > 0 && field.Type.Nullable && !field.Required {
			view.EmptyAsMissing = append(view.EmptyAsMissing, tsQuote(field.Name))
		}
		for _, rule := range field.Rules {
			line, err := queryRule(field, rule)
			if err != nil {
				return view, err
			}
			view.Rules = append(view.Rules, line)
		}
		if field.HasDefault {
			if err := validateQueryDefault(field); err != nil {
				return view, err
			}
		}
		switch field.Type.Kind {
		case "string", "number", "boolean", "Guid", "Date", "DateOnly", "TimeOnly", "TimeSpan", "enum":
		case "array":
			if field.Type.Element == nil || field.Type.Element.Kind == "model" || field.Type.Element.Kind == "record" {
				return view, fmt.Errorf("unsupported query parameter collection")
			}
		default:
			return view, fmt.Errorf("unsupported query parameter wire kind %q", field.Type.Kind)
		}
		if field.Required {
			required = append(required, tsQuote(field.Name))
		}
	}
	if query.PortableRules != (len(view.Rules) > 0) {
		return view, fmt.Errorf("query portable rule metadata disagrees with parameter rules")
	}
	if len(view.Rules) > 0 {
		validator = imports.add("@cratis/arc/queries", "QueryValidator", true)
	}
	key := "query:" + query.Declaration.Identity()
	localPaths := map[string]string{}
	for key, value := range paths {
		localPaths[key] = value
	}
	localPaths[key] = file
	fields := append([]FieldDescriptor(nil), query.Parameters...)
	fields = append(fields, FieldDescriptor{Name: "__arcResult", Type: query.Result})
	if view.Observable && view.Enumerable {
		fields = append(fields, FieldDescriptor{Name: "__arcItem", Type: *query.Result.Element})
	}
	planned, err := planModelUsingImports(TypeDescriptor{Key: key, Name: metadata.TypeName{Name: view.Name}, Kind: "model", Fields: fields}, nodes, localPaths, imports, false)
	if err != nil {
		return view, err
	}
	view.Imports, view.Base, view.Descriptor = planned.Imports, imports.aliases[base], imports.aliases[descriptor]
	view.Actions, view.QueryActions = imports.aliases[actions], imports.aliases[queryActions]
	view.Validator = imports.aliases[validator]
	if preference != "" {
		view.HTTP = imports.aliases[http] + "." + preference
	}
	response := planned.Properties[len(query.Parameters)]
	view.Data, view.Constructor = response.Type, response.Constructor
	if view.Observable && view.Enumerable {
		view.Item = planned.Properties[len(query.Parameters)+1].Type
	}
	for index, property := range planned.Properties[:len(query.Parameters)] {
		optional := ""
		if !query.Parameters[index].Required {
			optional = "?"
		}
		view.Parameters = append(view.Parameters, tsQueryParameter{property, tsQuote(query.Parameters[index].Name), optional})
	}
	view.Required = "[" + strings.Join(required, ", ") + "]"
	if len(view.Parameters) > 0 {
		view.ParametersName = view.Name + "Parameters"
	}
	if view.Observable {
		view.Hooks = observableQueryHooks(view, hookImports, imports)
	} else {
		view.Hooks = queryHooks(view, hookImports, imports)
	}
	return view, nil
}

// queryRule admits only the snapshot string family with paired server/client
// evidence. Compiler representation facts must survive either graph format:
// NewPortable inspects the Go value, not ConceptValue or a custom wire codec.
// Missing optional non-nullable Go strings become empty strings, while
// the client sees undefined; defaults are also applied only on the server. Refuse
// those shapes rather than silently changing the rules or the query arguments.
// The generated validator copies nullable optional arguments and maps empty
// strings to undefined, matching builtin GET/QUERY binding before portable rules.
func queryRule(field FieldDescriptor, rule validation.RuleDescriptor) (string, error) {
	if field.Type.Kind != "string" {
		return "", fmt.Errorf("query parameter %q: rules require paired client semantics for scalar strings", field.Name)
	}
	if field.QueryRules == nil || field.QueryRules.GoKind != "string" || field.QueryRules.CustomCodec || field.QueryRules.PointerDepth > 1 {
		return "", fmt.Errorf("query parameter %q: portable rules require proven Go string representation without custom codecs", field.Name)
	}
	if field.Binding != nil && (field.Binding.Reader != "builtin" || field.Binding.PreservePresence || !field.Binding.EmptyAsMissing || !field.Binding.NullAsMissing) {
		return "", fmt.Errorf("query parameter %q: portable rules require builtin empty/null-as-missing binding", field.Name)
	}
	if field.HasDefault || !field.Required && !field.Type.Nullable {
		return "", fmt.Errorf("query parameter %q: portable rules require required or nullable strings without server defaults", field.Name)
	}
	switch rule.Name {
	case "notNull", "notEmpty", "minLength", "maxLength", "length":
	default:
		return "", fmt.Errorf("query parameter %q: unsupported client portable rule %q", field.Name, rule.Name)
	}
	return commandRule(field, rule)
}

func tsQuoteList(values []string) string {
	quoted := []string{}
	for _, value := range values {
		quoted = append(quoted, tsQuote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func queryHooks(view tsQuery, keys map[string]tsImport, imports *tsImports) []string {
	alias := func(name string) string { return imports.aliases[keys[name]] }
	generic := view.Data + ", " + view.Name
	args, parameters, passed, perform := "", "", "", alias("PerformQuery")
	if view.ParametersName != "" {
		args = "args?: " + view.ParametersName
		parameters = ", " + view.ParametersName
		generic += parameters
		passed = ", args"
		perform += "<" + view.ParametersName + ">"
	}
	if view.Enumerable {
		if args != "" {
			args += ", "
		}
		args += "sorting?: " + alias("Sorting")
		if passed == "" {
			passed = ", undefined"
		}
		passed += ", sorting"
	}
	tuple := "[" + alias("QueryResultWithState") + "<" + view.Data + ">, " + perform + ", " + alias("SetSorting") + "]"
	lines := []string{}
	for _, pair := range [][2]string{{"use", "useQuery"}, {"useSuspense", "useSuspenseQuery"}} {
		lines = append(lines, "static "+pair[0]+"("+args+"): "+tuple+" {\n        return "+alias(pair[1])+"<"+generic+">("+view.Name+passed+");\n    }")
	}
	if view.Enumerable {
		pagingTuple := "[" + alias("QueryResultWithState") + "<" + view.Data + ">, " + alias("PerformQuery") + ", " + alias("SetSorting") + ", " + alias("SetPage") + ", " + alias("SetPageSize") + "]"
		for _, pair := range [][2]string{{"useWithPaging", "useQueryWithPaging"}, {"useSuspenseWithPaging", "useSuspenseQueryWithPaging"}} {
			lines = append(lines, "static "+pair[0]+"(pageSize: number, "+args+"): "+pagingTuple+" {\n        return "+alias(pair[1])+"<"+view.Data+", "+view.Name+">("+view.Name+", new "+alias("Paging")+"(0, pageSize)"+passed+");\n    }")
		}
	}
	when := alias("QueryWhen") + "<" + view.Name + ", " + view.Data + parameters + ">"
	lines = append(lines, "static when(condition: boolean): "+when+" {\n        return new "+when+"("+view.Name+", condition);\n    }")
	return lines
}

func queryReserved(name string) bool {
	if strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "constructor", "prototype", "__proto__", "route", "queryName", "validation", "roles", "parameterDescriptors", "requiredRequestParameters", "defaultValue", "treatWarningsAsErrors", "abortController", "sorting", "paging", "parameters", "modelType", "enumerable", "sortBy", "perform", "setMicroservice", "setApiBasePath", "setOrigin", "setHttpHeadersCallback", "setHttpMethod":
		return true
	}
	return false
}

// Defaults belong to server binding. Supported primitive defaults are validated
// but never assigned on the proxy, so omission still reaches the server as omission.
func validateQueryDefault(field FieldDescriptor) error {
	if field.Required {
		return fmt.Errorf("query parameter %q cannot be required and defaulted", field.Name)
	}
	switch field.Type.Kind {
	case "string":
		return nil
	case "boolean":
		if value := strings.ToLower(strings.TrimSpace(field.Default)); value == "true" || value == "false" {
			return nil
		}
	case "number":
		value, err := strconv.ParseFloat(field.Default, 64)
		if err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 9007199254740991 {
			return nil
		}
	}
	return fmt.Errorf("query parameter %q has unsupported server default", field.Name)
}
