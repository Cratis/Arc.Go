// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"slices"
	"strings"
)

// openAPIQueryShape is the declared request surface of one snapshot query: its
// builtin-bound arguments (sorted by wire name) and whether the result is
// pageable. The zero value is the argument-free, nonpageable checkpoint shape.
type openAPIQueryShape struct {
	arguments  []openAPIQueryArgument
	paged      bool
	sortFields []string
}

// openAPIQueryArgument renders one argument for both builtin readers. The GET
// form is a query-string parameter; the QUERY form is a member of the request
// body's arguments object. Both forms come from one Graph field descriptor.
type openAPIQueryArgument struct {
	name     string
	required bool
	get      openAPIObject
	body     openAPIObject
}

// openAPIQueryArguments admits only builtin-bound scalar and scalar-collection
// arguments. Presence-preserving bindings, validation rules, opaque codecs,
// models, maps and names that the GET reader treats as reserved controls are
// refused rather than described approximately.
func (r *openAPIRenderer) openAPIQueryArguments(parameters []FieldDescriptor) ([]openAPIQueryArgument, error) {
	ordered := slices.Clone(parameters)
	slices.SortFunc(ordered, func(a, b FieldDescriptor) int { return strings.Compare(a.Name, b.Name) })
	arguments := make([]openAPIQueryArgument, 0, len(ordered))
	seen := map[string]bool{}
	for _, field := range ordered {
		key := strings.ToLower(field.Name)
		if !openAPIArgumentName(field.Name) || seen[key] {
			return nil, fmt.Errorf("query argument %q requires a unique case-insensitive ASCII identifier", field.Name)
		}
		seen[key] = true
		if openAPIReservedQueryControl(field.Name) {
			return nil, fmt.Errorf("query argument %q collides with a reserved GET control", field.Name)
		}
		binding := field.Binding
		if binding == nil || binding.Reader != "builtin" {
			return nil, fmt.Errorf("query argument %q requires builtin binding metadata", field.Name)
		}
		if binding.PreservePresence || !binding.EmptyAsMissing || !binding.NullAsMissing {
			return nil, fmt.Errorf("query argument %q: presence-preserving binding is outside the published profile", field.Name)
		}
		if len(field.Rules) > 0 || field.QueryRules != nil {
			return nil, fmt.Errorf("query argument %q: validation rules need a witnessed metadata projection", field.Name)
		}
		value := openAPIQueryValueType(field.Type)
		var get, body openAPIObject
		switch binding.Encoding {
		case "scalar-text":
			typed, kind, err := r.openAPIQueryScalar(value)
			if err != nil {
				return nil, fmt.Errorf("query argument %q: %w", field.Name, err)
			}
			get = typed
			body = openAPIQueryTextForms(typed, kind)
		case "csv-or-json-array":
			if value.Kind != "array" || value.Element == nil {
				return nil, fmt.Errorf("query argument %q: collection binding requires an array descriptor", field.Name)
			}
			element, kind, err := r.openAPIQueryScalar(openAPIQueryValueType(*value.Element))
			if err != nil {
				return nil, fmt.Errorf("query argument %q element: %w", field.Name, err)
			}
			get = openAPIObject{"type": "array", "items": element}
			// JSON array nodes retain nulls for pointer and Optional elements;
			// GET/CSV elements are text and keep their nonnullable schema.
			items := openAPINull(openAPIQueryTextForms(element, kind), value.Element.Nullable || value.Element.Kind == "optional")
			array := openAPIObject{"type": "array", "items": items}
			if binding.FixedLength != nil {
				if *binding.FixedLength < 0 {
					return nil, fmt.Errorf("query argument %q: negative fixed length", field.Name)
				}
				get["minItems"], get["maxItems"] = *binding.FixedLength, *binding.FixedLength
				array["minItems"], array["maxItems"] = *binding.FixedLength, *binding.FixedLength
			}
			body = openAPIObject{"anyOf": []any{array, openAPIObject{"type": "string", "description": "Comma-separated text form; each element is trimmed and parsed like a GET value."}}}
		default:
			return nil, fmt.Errorf("query argument %q: unsupported binding encoding %q", field.Name, binding.Encoding)
		}
		required := false
		description := "Missing, empty and null values are treated as missing and bind the zero value."
		switch binding.Missing {
		case "error":
			required = true
			description = "Required. Missing, empty and null values produce a query validation error."
		case "default":
			description = "Missing, empty and null values bind the declared default."
		case "zero":
		default:
			return nil, fmt.Errorf("query argument %q: unsupported missing-value policy %q", field.Name, binding.Missing)
		}
		parameter := openAPIObject{"name": field.Name, "in": "query", "required": required, "schema": get, "description": description + " The name is matched case-insensitively; case-variant duplicates are malformed."}
		if binding.Encoding == "csv-or-json-array" {
			// Repeated values are comma-joined by the reader, so form/explode and
			// a single comma-separated value bind the same elements. Elements
			// cannot contain commas.
			parameter["style"], parameter["explode"] = "form", false
			parameter["description"] = parameter["description"].(string) + " Elements are comma-separated and trimmed; elements cannot contain commas."
		}
		arguments = append(arguments, openAPIQueryArgument{name: field.Name, required: required, get: parameter, body: openAPINull(body, true)})
	}
	return arguments, nil
}

// openAPIQueryValueType removes Optional wrappers. Pointer and Optional
// arguments share the reader's null/empty-as-missing behavior, which the
// argument descriptions state explicitly.
func openAPIQueryValueType(value WireType) WireType {
	for value.Kind == "optional" && value.Element != nil {
		value = *value.Element
	}
	value.Nullable = false
	return value
}

func (r *openAPIRenderer) openAPIQueryScalar(value WireType) (openAPIObject, string, error) {
	switch value.Kind {
	case "string", "boolean", "number":
	default:
		return nil, "", fmt.Errorf("unsupported argument wire kind %q; only string, boolean and number scalars are published", value.Kind)
	}
	schema, err := r.wire(value, "Input", false, false, 0)
	if err != nil {
		return nil, "", err
	}
	return schema, value.Kind, nil
}

// openAPIQueryTextForms describes the QUERY argument reader: JSON strings are
// parsed as text and other scalar tokens are bound by their JSON text, so a
// string argument also accepts numeric and boolean tokens.
func openAPIQueryTextForms(typed openAPIObject, kind string) openAPIObject {
	alternative := openAPIObject{"type": "string", "description": "Text form, parsed like the GET value."}
	if kind == "string" {
		alternative = openAPIObject{"type": []any{"number", "boolean"}, "description": "Scalar tokens bind their JSON text."}
	}
	return openAPIObject{"anyOf": []any{typed, alternative}}
}

// openAPIArgumentName keeps argument names valid in the case-alias patterns.
func openAPIArgumentName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func openAPIReservedQueryControl(name string) bool {
	switch strings.ToLower(name) {
	case "page", "pagesize", "sortby", "sortdirection", "waitforfirstresult", "waitforfirstresulttimeout":
		return true
	}
	return false
}

// openAPIQueryGETParameters describes the GET reader's paging and sorting
// controls for a pageable result. Nonpageable results publish no controls.
func openAPIQueryGETParameters(shape openAPIQueryShape) []any {
	if !shape.paged {
		return nil
	}
	integer := func() openAPIObject {
		return openAPIObject{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}
	}
	sortBy := openAPIObject{"type": "string"}
	if len(shape.sortFields) > 0 {
		sortBy["x-cratis-sort-fields"] = slices.Clone(shape.sortFields)
	}
	return []any{
		openAPIObject{"name": "page", "in": "query", "required": false, "schema": integer(), "description": "Zero-based page number. Used only when pageSize is present; malformed or out-of-range text is treated as zero."},
		openAPIObject{"name": "pageSize", "in": "query", "required": false, "schema": integer(), "description": "Text that parses as an int32 enables paging; other text leaves the query unpaged."},
		openAPIObject{"name": "sortBy", "in": "query", "required": false, "schema": sortBy, "description": "Sorting applies only when sortBy and sortDirection are both nonempty. An unknown active field produces a query validation error."},
		openAPIObject{"name": "sortDirection", "in": "query", "required": false, "schema": openAPIObject{"type": "string"}, "description": "asc, ascending, desc and descending are accepted case-insensitively; other values produce a sorting validation error when sortBy is nonempty."},
	}
}

// openAPIQueryRequest describes the built-in reader, not a fabricated request
// model. For the zero shape it describes the argument-free, nonpageable
// checkpoint; declared arguments and pageable results extend it.
//
// Source: Arc 7c1e780, Arc.Core/Queries/QueryRequestEnvelope.cs and
// BodyQueryRequestReader.cs; Go queries/request_query.go. The Go reader requires
// an object (unlike the C# null-envelope fallback), rejects duplicate known names
// case-insensitively, and rejects trailing JSON. Duplicate JSON members cannot be
// expressed in JSON Schema; neither can int32's integer-token spelling (1.0 and
// 1e0 are mathematical integers but malformed for the reader). Describe these
// parser boundaries rather than claiming schema validation proves acceptance.
// Sorting direction is a pipeline validation rule,
// not a JSON type: an unrecognized direction with a nonempty field returns 400.
func openAPIQueryRequest(shape openAPIQueryShape) openAPIObject {
	integer := func() openAPIObject {
		return openAPIObject{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}
	}
	paging := openAPIQueryObject(openAPIObject{"page": integer(), "pageSize": integer()})
	paging["description"] = "Only positive pageSize enables paging. Nonpageable snapshot results ignore valid paging. Missing integers default to zero; explicit null integers are malformed. JSON Schema checks mathematical integers, not token spelling: the reader rejects decimal/exponent tokens such as 1.0 and 1e0."
	if shape.paged {
		paging["description"] = "Only positive pageSize enables paging of this pageable result. Missing integers default to zero; explicit null integers are malformed. JSON Schema checks mathematical integers, not token spelling: the reader rejects decimal/exponent tokens such as 1.0 and 1e0."
	}
	sorting := openAPIQueryObject(openAPIObject{
		"field":     openAPINull(openAPIObject{"type": "string"}, true),
		"direction": openAPIObject{},
	})
	// With no matching field, or an empty/null field, this inner schema is
	// satisfied and its negation is false. A nonempty field (including any
	// reader-supported case alias) activates direction's nullable string shape.
	// Field's own type remains constrained even when direction is ignored.
	sorting["if"] = openAPIObject{"not": openAPIQueryObject(openAPIObject{
		"field": openAPIObject{"enum": []any{"", nil}},
	})}
	sorting["then"] = openAPIQueryObject(openAPIObject{
		"direction": openAPINull(openAPIObject{"type": "string"}, true),
	})
	sorting["description"] = "A nonempty field enables sorting; otherwise direction is ignored regardless of its JSON type. With a nonempty field, direction must be a string or null; omitted/null direction means ascending. asc, ascending, desc and descending are accepted case-insensitively; other strings then produce a query validation error. Nonpageable results ignore valid sorting."
	if shape.paged {
		sorting["description"] = "A nonempty field enables sorting; otherwise direction is ignored regardless of its JSON type. With a nonempty field, direction must be a string or null; omitted/null direction means ascending. asc, ascending, desc and descending are accepted case-insensitively; other strings, and an unknown active field, then produce a query validation error."
		if len(shape.sortFields) > 0 {
			sorting["x-cratis-sort-fields"] = slices.Clone(shape.sortFields)
		}
	}
	arguments := openAPIObject{"type": "object"}
	argumentsDescription := "This operation declares no arguments; supplied arguments do not become handler parameters."
	if len(shape.arguments) > 0 {
		properties := openAPIObject{}
		var required []string
		for _, argument := range shape.arguments {
			properties[argument.name] = argument.body
			if argument.required {
				required = append(required, argument.name)
			}
		}
		arguments = openAPIQueryObject(properties)
		arguments["description"] = "Declared arguments, matched case-insensitively. Null and empty-string values are treated as missing; unknown arguments are ignored."
		argumentsDescription = "Arguments bind the declared query parameters."
		if len(required) > 0 {
			// Names are case-insensitive, so a JSON Schema required list would
			// reject case variants the reader accepts. State requiredness instead.
			argumentsDescription += " Required arguments (" + strings.Join(required, ", ") + ") produce a query validation error when missing."
			arguments["x-cratis-required-arguments"] = required
		}
	}
	request := openAPIQueryObject(openAPIObject{
		"arguments": openAPINull(arguments, true),
		"paging":    openAPINull(paging, true),
		"sorting":   openAPINull(sorting, true),
	})
	request["description"] = "Exactly one JSON object. All members are optional; unknown members are ignored. Known member and argument names are case-insensitive; duplicate known members and duplicate argument names are malformed. Null arguments/paging/sorting act as omitted. " + argumentsDescription
	return request
}

// Keep canonical spelling visible to ordinary schema consumers while applying
// the same shape to case variants accepted by the built-in strings.ToLower
// reader. Go also maps U+0130 to i (and U+212A to k); these are not unknown
// members. Use ECMA-compatible character classes, not Go-only (?i).
func openAPIQueryObject(properties openAPIObject) openAPIObject {
	patterns := openAPIObject{}
	for name, schema := range properties {
		var pattern strings.Builder
		pattern.WriteByte('^')
		for _, c := range name {
			lower := strings.ToLower(string(c))
			upper := strings.ToUpper(string(c))
			extra := ""
			switch lower {
			case "i":
				extra = "İ"
			case "k":
				extra = "\u212A"
			}
			pattern.WriteString("[" + lower + upper + extra + "]")
		}
		pattern.WriteByte('$')
		patterns[pattern.String()] = schema
	}
	return openAPIObject{"type": "object", "properties": properties, "patternProperties": patterns}
}
