// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package capability_test

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The original upstream bytes (not schema-base); no publication refs are rewritten.
//
//go:embed official-3.1-schema.json
var officialSchemaBytes []byte

//go:embed official-3.1-LICENSE
var officialLicenseBytes []byte

const officialSchemaID = "https://spec.openapis.org/oas/3.1/schema/2026-08-03"
const officialOverlayID = "https://capability.invalid/query-overlay.json"

// This is a bounded capability experiment, not an exported/general validator.
// The overlay validates the actual QUERY instance in the WHOLE document.
const officialQueryOverlay = `{
 "$schema":"https://json-schema.org/draft/2020-12/schema",
 "type":"object","properties":{"paths":{"type":"object","additionalProperties":{
  "type":"object","properties":{"x-cratis-query":{
   "type":"object","required":["method","operation"],"properties":{
    "method":{"const":"QUERY"},
    "operation":{"$ref":"https://spec.openapis.org/oas/3.1/schema/2026-08-03#/$defs/operation"}
   },"unevaluatedProperties":false
  }}
 }}}
}`

// Admission is evaluated ONLY at actual Schema Objects. Assertion semantics go
// to the exact engine; never use a hand-written numeric post-filter.
var officialAdmittedKeywords = strings.Fields(`$schema $ref $defs $comment type enum const
 allOf anyOf oneOf not if then else properties patternProperties additionalProperties
 propertyNames required dependentRequired dependentSchemas unevaluatedProperties
 items prefixItems contains unevaluatedItems uniqueItems
 minimum maximum exclusiveMinimum exclusiveMaximum multipleOf
 minLength maxLength pattern minItems maxItems minProperties maxProperties minContains maxContains
 title description default examples example format readOnly writeOnly deprecated`)
var officialCountKeywords = strings.Fields("minLength maxLength minItems maxItems minProperties maxProperties minContains maxContains")
var officialSchemaSingles = strings.Fields("not if then else additionalProperties propertyNames unevaluatedProperties items contains unevaluatedItems")
var officialSchemaMaps = strings.Fields("$defs properties patternProperties dependentSchemas")
var officialSchemaArrays = strings.Fields("allOf anyOf oneOf prefixItems")
var officialMethods = strings.Fields("get put post delete options head patch trace")
var officialFormats = strings.Fields("date time date-time duration email hostname ipv4 ipv6 uri uri-reference uuid regex json-pointer relative-json-pointer int32 int64 uint64 float double")
var officialAnnotationFormats = strings.Fields("int32 int64 uint64 float double")
var officialMediaKeys = []string{"application/json", "application/problem+json", "text/plain", "text/event-stream"}

type officialOperation struct{ pointer, pathItem, path string }
type officialInventory struct {
	objects    map[string]string
	schemas    []string
	operations []officialOperation
	media      []string
	refs       map[string]string
}

func officialObject(value any) map[string]any { object, _ := value.(map[string]any); return object }
func officialKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
func officialPointer(parent, key string) string {
	return parent + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
}
func officialLocalPointer(ref string) (string, error) {
	if !strings.HasPrefix(ref, "#/") {
		return "", fmt.Errorf("only local JSON Pointer references admitted: %q", ref)
	}
	pointer, err := url.PathUnescape(strings.TrimPrefix(ref, "#"))
	if err != nil {
		return "", err
	}
	for _, segment := range strings.Split(pointer[1:], "/") {
		for index := 0; index < len(segment); index++ {
			if segment[index] == '~' {
				if index+1 == len(segment) || (segment[index+1] != '0' && segment[index+1] != '1') {
					return "", fmt.Errorf("invalid pointer escape: %s", ref)
				}
				index++
			}
		}
	}
	// Canonical pointer still escapes literal '/' and '~'; percent escapes are decoded.
	return pointer, nil
}
func officialAt(document map[string]any, pointer string) (any, error) {
	var current any = document
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token = strings.NewReplacer("~1", "/", "~0", "~").Replace(token)
		switch value := current.(type) {
		case map[string]any:
			var exists bool
			current, exists = value[token]
			if !exists {
				return nil, fmt.Errorf("missing pointer %s", pointer)
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(value) || strconv.Itoa(index) != token {
				return nil, fmt.Errorf("invalid array pointer %s", pointer)
			}
			current = value[index]
		default:
			return nil, fmt.Errorf("non-container pointer %s", pointer)
		}
	}
	return current, nil
}

// Context traversal, never an indiscriminate search for keys named 'schema' or
// '$ref'. Examples/defaults/extensions are instance data, not schema positions.
func officialDiscover(document map[string]any) (*officialInventory, error) {
	inventory := &officialInventory{objects: map[string]string{}, refs: map[string]string{}}
	var walk func(any, string, string, string, string, int) error
	walk = func(value any, pointer, kind, pathItem, path string, depth int) error {
		if depth > 128 || len(inventory.objects) >= 10000 {
			return fmt.Errorf("capability inventory budget exceeded at %s", pointer)
		}
		inventory.objects[pointer] = kind
		object := officialObject(value)
		if kind == "schema" {
			inventory.schemas = append(inventory.schemas, pointer)
			if _, ok := value.(bool); ok {
				return nil
			}
			if object == nil {
				return fmt.Errorf("schema is not object/boolean at %s", pointer)
			}
			for _, key := range officialKeys(object) {
				if !slices.Contains(officialAdmittedKeywords, key) {
					return fmt.Errorf("unsupported schema keyword %s at %s", key, pointer)
				}
				if key == "$schema" && object[key] != exactDialect {
					return fmt.Errorf("alternate schema dialect at %s", pointer)
				}
				if slices.Contains(officialCountKeywords, key) {
					number, ok := object[key].(json.Number)
					if !ok {
						return fmt.Errorf("non-numeric count %s at %s", key, pointer)
					}
					rat, ok := new(big.Rat).SetString(string(number))
					max := new(big.Int).SetUint64(uint64(^uint(0) >> 1))
					if !ok || !rat.IsInt() || rat.Sign() < 0 || rat.Num().Cmp(max) > 0 {
						return fmt.Errorf("count does not fit nonnegative Go int: %s at %s", key, pointer)
					}
				}
				if key == "format" {
					format, ok := object[key].(string)
					if !ok || !slices.Contains(officialFormats, format) {
						return fmt.Errorf("unsupported format at %s", pointer)
					}
				}
				if slices.Contains(officialSchemaSingles, key) {
					if err := walk(object[key], officialPointer(pointer, key), "schema", "", "", depth+1); err != nil {
						return err
					}
				}
				if slices.Contains(officialSchemaMaps, key) {
					children := officialObject(object[key])
					if children == nil {
						return fmt.Errorf("schema map %s at %s", key, pointer)
					}
					for _, name := range officialKeys(children) {
						if err := walk(children[name], officialPointer(officialPointer(pointer, key), name), "schema", "", "", depth+1); err != nil {
							return err
						}
					}
				}
				if slices.Contains(officialSchemaArrays, key) {
					children, ok := object[key].([]any)
					if !ok {
						return fmt.Errorf("schema array %s at %s", key, pointer)
					}
					for index, child := range children {
						if err := walk(child, officialPointer(officialPointer(pointer, key), strconv.Itoa(index)), "schema", "", "", depth+1); err != nil {
							return err
						}
					}
				}
			}
		}
		if object == nil {
			return fmt.Errorf("%s object at %s", kind, pointer)
		}
		if ref, exists := object["$ref"]; exists {
			text, ok := ref.(string)
			if !ok {
				return fmt.Errorf("non-string ref at %s", pointer)
			}
			target, err := officialLocalPointer(text)
			if err != nil {
				return fmt.Errorf("%s: %w", pointer, err)
			}
			inventory.refs[pointer] = target
		}
		child := func(key, childKind string) error {
			if value, ok := object[key]; ok {
				return walk(value, officialPointer(pointer, key), childKind, pathItem, path, depth+1)
			}
			return nil
		}
		children := func(key, childKind string) error {
			if value, ok := object[key]; ok {
				children := officialObject(value)
				if children == nil {
					return fmt.Errorf("%s map at %s", key, pointer)
				}
				for _, name := range officialKeys(children) {
					// Responses is an extensible Object. The other fields here are
					// name-to-object maps: x- names are entries, not extensions.
					if kind == "operation" && key == "responses" && strings.HasPrefix(name, "x-") {
						continue
					}
					if err := walk(children[name], officialPointer(officialPointer(pointer, key), name), childKind, pathItem, path, depth+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		parameters := func() error {
			if value, ok := object["parameters"]; ok {
				list, ok := value.([]any)
				if !ok {
					return fmt.Errorf("parameters array at %s", pointer)
				}
				for index, value := range list {
					if err := walk(value, officialPointer(officialPointer(pointer, "parameters"), strconv.Itoa(index)), "parameter", pathItem, path, depth+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		switch kind {
		case "schema":
			return nil
		case "path-item":
			for _, method := range officialMethods {
				if value, exists := object[method]; exists {
					if err := walk(value, officialPointer(pointer, method), "operation", pointer, path, depth+1); err != nil {
						return err
					}
				}
			}
			if value, exists := object["x-cratis-query"]; exists {
				if !strings.HasPrefix(pointer, "/paths/") {
					return fmt.Errorf("QUERY outside paths at %s", pointer)
				}
				envelope := officialObject(value)
				if envelope == nil {
					return fmt.Errorf("QUERY envelope at %s", pointer)
				}
				if operation, exists := envelope["operation"]; exists {
					if err := walk(operation, officialPointer(officialPointer(pointer, "x-cratis-query"), "operation"), "operation", pointer, path, depth+1); err != nil {
						return err
					}
				}
			}
			return parameters()
		case "operation":
			inventory.operations = append(inventory.operations, officialOperation{pointer, pathItem, path})
			if err := parameters(); err != nil {
				return err
			}
			if err := child("requestBody", "request-body"); err != nil {
				return err
			}
			if err := children("responses", "response"); err != nil {
				return err
			}
			return children("callbacks", "callback")
		case "callback":
			for _, expression := range officialKeys(object) {
				if expression == "$ref" || expression == "summary" || expression == "description" || strings.HasPrefix(expression, "x-") {
					continue
				}
				if err := walk(object[expression], officialPointer(pointer, expression), "path-item", "", expression, depth+1); err != nil {
					return err
				}
			}
			return nil
		case "response":
			if err := children("headers", "header"); err != nil {
				return err
			}
			if err := children("links", "link"); err != nil {
				return err
			}
			return children("content", "media-type")
		case "request-body":
			return children("content", "media-type")
		case "parameter", "header":
			if err := child("schema", "schema"); err != nil {
				return err
			}
			if err := children("content", "media-type"); err != nil {
				return err
			}
			return children("examples", "example")
		case "media-type":
			inventory.media = append(inventory.media, pointer)
			key := strings.Split(pointer, "/")
			mediaKey := strings.NewReplacer("~1", "/", "~0", "~").Replace(key[len(key)-1])
			if !slices.Contains(officialMediaKeys, mediaKey) {
				return fmt.Errorf("unsupported renderer content key %q at %s", mediaKey, pointer)
			}
			if err := child("schema", "schema"); err != nil {
				return err
			}
			if err := children("examples", "example"); err != nil {
				return err
			}
			return children("encoding", "encoding")
		case "encoding":
			return children("headers", "header")
		case "link":
			if ref, exists := object["operationRef"]; exists {
				text, ok := ref.(string)
				if !ok {
					return fmt.Errorf("operationRef at %s", pointer)
				}
				target, err := officialLocalPointer(text)
				if err != nil {
					return err
				}
				inventory.refs[pointer+"/operationRef"] = target
			}
		}
		return nil
	}
	for _, group := range []string{"paths", "webhooks"} {
		if paths, exists := document[group]; exists {
			object := officialObject(paths)
			if object == nil {
				return nil, fmt.Errorf("%s map", group)
			}
			for _, path := range officialKeys(object) {
				// Paths is extensible; webhooks is a name-to-Path-Item map.
				if group == "paths" && strings.HasPrefix(path, "x-") {
					continue
				}
				if err := walk(object[path], officialPointer("/"+group, path), "path-item", "", path, 0); err != nil {
					return nil, err
				}
			}
		}
	}
	componentKinds := map[string]string{"schemas": "schema", "responses": "response", "parameters": "parameter", "examples": "example", "requestBodies": "request-body", "headers": "header", "securitySchemes": "security-scheme", "links": "link", "callbacks": "callback", "pathItems": "path-item"}
	components := officialObject(document["components"])
	for _, group := range officialKeys(componentKindsAny(componentKinds)) {
		if values, exists := components[group]; exists {
			objects := officialObject(values)
			if objects == nil {
				return nil, fmt.Errorf("component map %s", group)
			}
			for _, name := range officialKeys(objects) {
				if err := walk(objects[name], officialPointer("/components/"+group, name), componentKinds[group], "", "", 0); err != nil {
					return nil, err
				}
			}
		}
	}
	slices.Sort(inventory.schemas)
	slices.Sort(inventory.media)
	return inventory, nil
}
func componentKindsAny(kinds map[string]string) map[string]any {
	result := map[string]any{}
	for key, value := range kinds {
		result[key] = value
	}
	return result
}

func (inventory *officialInventory) resolve(document map[string]any, pointer, kind string) (map[string]any, error) {
	seen := map[string]bool{}
	for {
		if seen[pointer] {
			return nil, fmt.Errorf("unproductive %s Reference Object cycle at %s", kind, pointer)
		}
		seen[pointer] = true
		if inventory.objects[pointer] != kind {
			return nil, fmt.Errorf("reference target %s is %s, want %s", pointer, inventory.objects[pointer], kind)
		}
		value, err := officialAt(document, pointer)
		if err != nil {
			return nil, err
		}
		object := officialObject(value)
		if target, exists := inventory.refs[pointer]; exists {
			pointer = target
			continue
		}
		return object, nil
	}
}

func officialRelationships(document map[string]any, inventory *officialInventory) error {
	for _, pointer := range officialKeys(componentKindsAny(inventory.refs)) {
		target := inventory.refs[pointer]
		kind := inventory.objects[pointer]
		if strings.HasSuffix(pointer, "/operationRef") {
			kind = "operation"
		}
		if inventory.objects[target] != kind {
			return fmt.Errorf("typed local reference %s -> %s (want %s, got %s)", pointer, target, kind, inventory.objects[target])
		}
		if kind != "schema" {
			start := pointer
			if strings.HasSuffix(pointer, "/operationRef") {
				start = target
			}
			if _, err := inventory.resolve(document, start, kind); err != nil {
				return err
			}
		}
	}
	securitySchemes := officialObject(officialObject(document["components"])["securitySchemes"])
	checkSecurity := func(value any) error {
		list, ok := value.([]any)
		if !ok {
			return fmt.Errorf("security not array")
		}
		for _, requirement := range list {
			object := officialObject(requirement)
			if object == nil {
				return fmt.Errorf("security requirement not object")
			}
			for _, name := range officialKeys(object) {
				if _, exists := securitySchemes[name]; !exists {
					return fmt.Errorf("security scheme %q missing", name)
				}
				if _, err := inventory.resolve(document, officialPointer("/components/securitySchemes", name), "security-scheme"); err != nil {
					return err
				}
			}
		}
		return nil // Non-OAuth role arrays are valid in OpenAPI 3.1.1.
	}
	if value, exists := document["security"]; exists {
		if err := checkSecurity(value); err != nil {
			return err
		}
	}
	ids := map[string]string{}
	for _, operation := range inventory.operations {
		value, err := officialAt(document, operation.pointer)
		if err != nil {
			return err
		}
		object := officialObject(value)
		if id, exists := object["operationId"].(string); exists {
			if previous, duplicate := ids[id]; duplicate {
				return fmt.Errorf("duplicate operationId %q: %s and %s", id, previous, operation.pointer)
			}
			ids[id] = operation.pointer
		}
		if value, exists := object["security"]; exists {
			if err := checkSecurity(value); err != nil {
				return fmt.Errorf("%s: %w", operation.pointer, err)
			}
		}
		pathObject, err := inventory.resolve(document, operation.pathItem, "path-item")
		if err != nil {
			return err
		}
		inherited, err := officialParameters(document, inventory, pathObject)
		if err != nil {
			return err
		}
		own, err := officialParameters(document, inventory, object)
		if err != nil {
			return err
		}
		for key, value := range own {
			inherited[key] = value
		} // Legal operation override.
		if strings.HasPrefix(operation.pathItem, "/paths/") {
			names := map[string]bool{}
			for _, match := range regexp.MustCompile(`\{([^{}]+)\}`).FindAllStringSubmatch(operation.path, -1) {
				names[match[1]] = true
			}
			for key, parameter := range inherited {
				if parameter["in"] == "path" {
					name, _ := parameter["name"].(string)
					if !names[name] {
						return fmt.Errorf("path parameter %s not in template %s", key, operation.path)
					}
					delete(names, name)
				}
			}
			if len(names) != 0 {
				return fmt.Errorf("missing path-template parameters at %s", operation.pointer)
			}
		}
	}
	// Also validate duplicate arrays on unused component Path Items/Operations.
	for pointer, kind := range inventory.objects {
		if kind == "path-item" || kind == "operation" {
			value, err := officialAt(document, pointer)
			if err != nil {
				return err
			}
			if _, err := officialParameters(document, inventory, officialObject(value)); err != nil {
				return err
			}
		}
	}
	for pointer, kind := range inventory.objects {
		if kind == "link" {
			value, _ := officialAt(document, pointer)
			object := officialObject(value)
			if id, exists := object["operationId"].(string); exists {
				if _, ok := ids[id]; !ok {
					return fmt.Errorf("unknown link operationId at %s", pointer)
				}
			}
		}
	}
	return nil
}
func officialParameters(document map[string]any, inventory *officialInventory, object map[string]any) (map[string]map[string]any, error) {
	result := map[string]map[string]any{}
	list, _ := object["parameters"].([]any)
	for _, value := range list {
		parameter := officialObject(value)
		if ref, ok := parameter["$ref"].(string); ok {
			pointer, err := officialLocalPointer(ref)
			if err != nil {
				return nil, err
			}
			parameter, err = inventory.resolve(document, pointer, "parameter")
			if err != nil {
				return nil, err
			}
		}
		name, _ := parameter["name"].(string)
		in, _ := parameter["in"].(string)
		key := name + "\x00" + in
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate resolved parameter (%s,%s)", name, in)
		}
		result[key] = parameter
	}
	return result, nil
}

type officialCheckResult struct {
	document  OpenAPIDocument
	inventory *officialInventory
	compiled  map[string]*jsonschema.Schema
}

func officialCompiler(calls *int, assertFormats bool) *jsonschema.Compiler {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(refusingLoader{calls})
	// v6.0.3 delegates these formats to net/url, whose accepted syntax is
	// broader than RFC 3986. Register grammar assertions before compilation;
	// the official schema bytes and the caller's instance remain unchanged.
	compiler.RegisterFormat(&jsonschema.Format{Name: "uri", Validate: officialURIFormat(false)})
	compiler.RegisterFormat(&jsonschema.Format{Name: "uri-reference", Validate: officialURIFormat(true)})
	if assertFormats {
		compiler.AssertFormat()
	}
	return compiler
}
func officialStructure(calls *int) (*jsonschema.Schema, *jsonschema.Schema, error) {
	compiler := officialCompiler(calls, true)
	resource, err := jsonschema.UnmarshalJSON(bytes.NewReader(officialSchemaBytes))
	if err != nil {
		return nil, nil, err
	}
	if err := compiler.AddResource(officialSchemaID, resource); err != nil {
		return nil, nil, err
	}
	overlay, err := jsonschema.UnmarshalJSON(strings.NewReader(officialQueryOverlay))
	if err != nil {
		return nil, nil, err
	}
	if err := compiler.AddResource(officialOverlayID, overlay); err != nil {
		return nil, nil, err
	}
	structure, err := compiler.Compile(officialSchemaID)
	if err != nil {
		return nil, nil, err
	}
	query, err := compiler.Compile(officialOverlayID)
	return structure, query, err
}
func officialCheck(data []byte, structure, query *jsonschema.Schema, calls *int, assertFormats bool) (*officialCheckResult, error) {
	document, err := decodeExactDocument(data)
	if err != nil {
		return nil, err
	}
	if document.value["openapi"] != "3.1.1" || document.value["jsonSchemaDialect"] != exactDialect {
		return nil, fmt.Errorf("capability requires OpenAPI 3.1.1 and explicit Draft 2020-12")
	}
	if err := structure.Validate(document.value); err != nil {
		return nil, fmt.Errorf("official structure: %w", err)
	}
	if err := query.Validate(document.value); err != nil {
		return nil, fmt.Errorf("QUERY overlay: %w", err)
	}
	inventory, err := officialDiscover(document.value)
	if err != nil {
		return nil, err
	}
	if err := officialRelationships(document.value, inventory); err != nil {
		return nil, err
	}
	compiler := officialCompiler(calls, assertFormats)
	if err := compiler.AddResource(exactResource, document.value); err != nil {
		return nil, err
	}
	result := &officialCheckResult{document, inventory, map[string]*jsonschema.Schema{}}
	for _, pointer := range inventory.schemas {
		schema, err := compiler.Compile(exactResource + "#" + pointer)
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", pointer, err)
		}
		result.compiled[pointer] = schema
		value, _ := officialAt(document.value, pointer)
		object := officialObject(value)
		for _, key := range []string{"default", "example"} {
			if value, exists := object[key]; exists {
				if err := schema.Validate(value); err != nil {
					return nil, fmt.Errorf("%s/%s: %w", pointer, key, err)
				}
			}
		}
		if examples, ok := object["examples"].([]any); ok {
			for index, value := range examples {
				if err := schema.Validate(value); err != nil {
					return nil, fmt.Errorf("%s/examples/%d: %w", pointer, index, err)
				}
			}
		}
	}
	// OpenAPI examples are instance data; their contents NEVER become schemas.
	for pointer, kind := range inventory.objects {
		if kind != "media-type" && kind != "parameter" && kind != "header" {
			continue
		}
		value, _ := officialAt(document.value, pointer)
		object := officialObject(value)
		schema := result.compiled[pointer+"/schema"]
		if schema == nil {
			continue
		}
		if example, exists := object["example"]; exists {
			if err := schema.Validate(example); err != nil {
				return nil, fmt.Errorf("%s/example: %w", pointer, err)
			}
		}
		for name, value := range officialObject(object["examples"]) {
			example := officialObject(value)
			if ref, ok := example["$ref"].(string); ok {
				target, err := officialLocalPointer(ref)
				if err != nil {
					return nil, err
				}
				example, err = inventory.resolve(document.value, target, "example")
				if err != nil {
					return nil, err
				}
			}
			if value, exists := example["value"]; exists {
				if err := schema.Validate(value); err != nil {
					return nil, fmt.Errorf("%s/examples/%s: %w", pointer, name, err)
				}
			}
		}
	}
	return result, nil
}

func officialHarness(t *testing.T) (*jsonschema.Schema, *jsonschema.Schema, *int) {
	t.Helper()
	calls := new(int)
	structure, query, err := officialStructure(calls)
	if err != nil {
		t.Fatalf("STOP official schema compile: %v", err)
	}
	if *calls != 0 {
		t.Fatalf("trusted official compile attempted I/O: %d", *calls)
	}
	return structure, query, calls
}
func officialData(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	document, err := decodeExactDocument([]byte(exactFixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(document.value)
	}
	data, err := document.bytes()
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func officialOperationObject(document map[string]any, query bool) map[string]any {
	paths := officialObject(document["paths"])
	if query {
		return officialObject(officialObject(officialObject(paths["/query"])["x-cratis-query"])["operation"])
	}
	return officialObject(officialObject(paths["/native"])["get"])
}
func officialSchemas(document map[string]any) map[string]any {
	return officialObject(officialObject(document["components"])["schemas"])
}
func officialAssertCase(t *testing.T, data []byte, valid bool, structure, query *jsonschema.Schema, calls *int) *officialCheckResult {
	t.Helper()
	before := bytes.Clone(data)
	result, err := officialCheck(data, structure, query, calls, true)
	if (err == nil) != valid {
		t.Fatalf("STOP unexpected acceptance/rejection: valid=%t want=%t: %v", err == nil, valid, err)
	}
	if *calls != 0 || !bytes.Equal(before, data) {
		t.Fatalf("I/O calls=%d or authoritative bytes mutated", *calls)
	}
	if err != nil {
		t.Logf("refused: %v", err)
	}
	return result
}

func TestOfficialCapabilityPinnedResources(t *testing.T) {
	for _, fixture := range []struct {
		name string
		data []byte
		hash string
		size int
	}{
		{"schema", officialSchemaBytes, "59f106413cb48c31299f96f024c938d3628aed6cd02cd14bcfb2fcaae7a130b6", 33483},
		{"Apache-2.0 LICENSE", officialLicenseBytes, "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4", 11357},
		{"URI tests", officialURITests, "47954ee6aef87c20a045ad2182fecd9c7eec53b6c8151ee88cba50381b2850a4", 8893},
		{"URI-reference tests", officialURIReferenceTests, "3a9913d43edd31650d3c4f8cc75b2b701bd6670c514cc759f4586245f72c59cb", 7771},
		{"URI tests MIT LICENSE", officialURITestsLicense, "837402bd25fad9b704265801ca3f92566a98157c1f9a7acd6f446299ba1c305a", 1057},
	} {
		if len(fixture.data) != fixture.size || fmt.Sprintf("%x", sha256.Sum256(fixture.data)) != fixture.hash {
			t.Fatalf("raw pinned %s changed", fixture.name)
		}
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(officialSchemaBytes))
	if err != nil || officialObject(value)["$id"] != officialSchemaID {
		t.Fatal("original official $id changed")
	}
	officialHarness(t)
	t.Log("checked 5 raw pinned resources, trusted official dynamic-reference infrastructure compiled offline")
}

func TestOfficialCapabilityExactNumbersAndInventory(t *testing.T) {
	structure, query, calls := officialHarness(t)
	data := officialData(t, nil)
	result := officialAssertCase(t, data, true, structure, query, calls)
	expected := slices.Clone(exactSchemaPointers)
	slices.Sort(expected)
	if !reflect.DeepEqual(result.inventory.schemas, expected) {
		t.Fatalf("STOP incomplete inventory: got %v want %v", result.inventory.schemas, expected)
	}
	for _, pointer := range expected {
		if result.compiled[pointer].Location != exactResource+"#"+pointer {
			t.Fatalf("original pointer lost: %s", pointer)
		}
	}
	for _, tc := range []struct{ schema, keyword, want string }{
		{"Signed", "minimum", "-9223372036854775808"}, {"Signed", "maximum", "9223372036854775807"}, {"Unsigned", "maximum", "18446744073709551615"},
	} {
		number, ok := officialObject(officialSchemas(result.document.value)[tc.schema])[tc.keyword].(json.Number)
		if !ok {
			t.Fatal("physical token not json.Number")
		}
		got, ok := new(big.Rat).SetString(string(number))
		want, _ := new(big.Rat).SetString(tc.want)
		if !ok || got.Cmp(want) != 0 {
			t.Fatal("physical bound not exact")
		}
	}
	for _, tc := range []struct {
		name, schema, value string
		valid               bool
	}{
		{"signed_min", "Signed", "-9223372036854775808", true}, {"signed_max", "Signed", "9223372036854775807", true}, {"unsigned_zero", "Unsigned", "0", true}, {"unsigned_max", "Unsigned", "18446744073709551615", true},
		{"below_signed", "Signed", "-9223372036854775809", false}, {"above_signed", "Signed", "9223372036854775808", false}, {"above_unsigned", "Unsigned", "18446744073709551616", false}, {"negative_unsigned", "Unsigned", "-1", false}, {"fraction", "Unsigned", "0.5", false},
		{"small_min", "Small", "-128", true}, {"small_max", "Small", "127", true}, {"small_below", "Small", "-129", false}, {"small_above", "Small", "128", false},
		{"exclusive_inside", "Exclusive", "9223372036854775808", true}, {"exclusive_lower", "Exclusive", "9223372036854775807", false}, {"exclusive_upper", "Exclusive", "9223372036854775809", false},
		{"multiple", "Multiple", "0.3", true}, {"not_multiple", "Multiple", "0.31", false}, {"const", "Constant", "18446744073709551615", true}, {"not_const", "Constant", "18446744073709551616", false},
		{"enum_signed", "Enumeration", "-9223372036854775808", true}, {"enum_unsigned", "Enumeration", "18446744073709551615", true}, {"not_enum", "Enumeration", "18446744073709551616", false},
		{"null", "Wrapper", `{"value":null}`, true}, {"string", "Wrapper", `{"value":"yes"}`, true}, {"not_nullable", "Wrapper", `{"value":true}`, false}, {"missing", "Wrapper", `{}`, false},
		{"recursive", "Recursive", `{"next":{"next":null}}`, true}, {"bad_recursive", "Recursive", `{"next":{"next":2}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.value))
			if err != nil {
				t.Fatal(err)
			}
			err = result.compiled["/components/schemas/"+tc.schema].Validate(value)
			if (err == nil) != tc.valid {
				t.Fatalf("STOP exact instance %s: %v", tc.value, err)
			}
		})
	}
	for _, pointer := range exactSchemaPointers[len(exactSchemaPointers)-2:] {
		for _, tc := range []struct {
			number string
			valid  bool
		}{{"18446744073709551615", true}, {"18446744073709551616", false}} {
			err := result.compiled[pointer].Validate(json.Number(tc.number))
			if (err == nil) != tc.valid {
				t.Fatalf("STOP inline/ref %s: %v", pointer, err)
			}
		}
	}
	t.Logf("compiled complete expected set: %d original Schema Object pointers; %d ordinary/QUERY operations", len(expected), len(result.inventory.operations))
}

func TestOfficialCapabilityOperationStructure(t *testing.T) {
	structure, query, calls := officialHarness(t)
	for _, isQuery := range []bool{false, true} {
		label := "native"
		if isQuery {
			label = "QUERY"
		}
		t.Run(label, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				value any
				valid bool
			}{
				{"default", map[string]any{"default": map[string]any{"description": ""}}, true},
				{"range_and_extension", map[string]any{"2XX": map[string]any{"description": ""}, "x-note": map[string]any{"anything": true}}, true},
				{"numeric", map[string]any{"599": map[string]any{"description": ""}}, true},
				{"wrong", map[string]any{"wrong": map[string]any{"description": "bad"}}, false},
				{"extensions_only", map[string]any{"x-note": true}, false},
				{"empty", map[string]any{}, false}, {"lower_range", map[string]any{"2xx": map[string]any{"description": ""}}, false},
				{"600", map[string]any{"600": map[string]any{"description": ""}}, false}, {"099", map[string]any{"099": map[string]any{"description": ""}}, false},
				{"status_suffix", map[string]any{"200abc": map[string]any{"description": ""}}, false},
				{"wrong_value", map[string]any{"200": "bad"}, false}, {"missing_description", map[string]any{"200": map[string]any{}}, false},
				{"wrong_description", map[string]any{"200": map[string]any{"description": true}}, false},
				{"response_unknown", map[string]any{"200": map[string]any{"description": "", "wrong": true}}, false},
			} {
				t.Run("responses_"+tc.name, func(t *testing.T) {
					data := officialData(t, func(document map[string]any) { officialOperationObject(document, isQuery)["responses"] = tc.value })
					officialAssertCase(t, data, tc.valid, structure, query, calls)
				})
			}
			for _, tc := range []struct {
				name   string
				mutate func(map[string]any)
				valid  bool
			}{
				{"responses_optional", func(op map[string]any) { delete(op, "responses") }, true},
				{"all_fields", func(op map[string]any) {
					op["tags"] = []any{"tag"}
					op["summary"] = ""
					op["description"] = ""
					op["deprecated"] = true
					op["externalDocs"] = map[string]any{"url": "https://example.invalid/docs"}
					op["servers"] = []any{map[string]any{"url": "/"}}
					op["security"] = []any{}
					op["parameters"] = []any{}
					op["requestBody"] = map[string]any{"required": false, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "string"}, "example": "ok"}}}
					op["callbacks"] = map[string]any{}
					op["x-note"] = true
				}, true},
				{"tags_type", func(op map[string]any) { op["tags"] = "bad" }, false},
				{"summary_type", func(op map[string]any) { op["summary"] = true }, false},
				{"description_type", func(op map[string]any) { op["description"] = true }, false},
				{"deprecated_type", func(op map[string]any) { op["deprecated"] = "true" }, false},
				{"external_docs", func(op map[string]any) { op["externalDocs"] = map[string]any{} }, false},
				{"external_docs_uri", func(op map[string]any) { op["externalDocs"] = map[string]any{"url": "not a uri with spaces"} }, false},
				{"opid_type", func(op map[string]any) { op["operationId"] = true }, false},
				{"unknown_field", func(op map[string]any) { op["unexpected"] = true }, false},
				{"parameters_type", func(op map[string]any) { op["parameters"] = map[string]any{} }, false},
				{"bad_parameter", func(op map[string]any) {
					op["parameters"] = []any{map[string]any{"name": "x", "in": "wrong", "schema": true}}
				}, false},
				{"request_body_type", func(op map[string]any) { op["requestBody"] = true }, false},
				{"request_body_missing_content", func(op map[string]any) { op["requestBody"] = map[string]any{} }, false},
				{"callbacks_type", func(op map[string]any) { op["callbacks"] = []any{} }, false},
				{"servers_type", func(op map[string]any) { op["servers"] = true }, false},
				{"server_missing_url", func(op map[string]any) { op["servers"] = []any{map[string]any{}} }, false},
				{"security_type", func(op map[string]any) { op["security"] = true }, false},
				{"operation_ref_object", func(op map[string]any) { op["$ref"] = "#/components/schemas/Signed" }, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					data := officialData(t, func(document map[string]any) { tc.mutate(officialOperationObject(document, isQuery)) })
					officialAssertCase(t, data, tc.valid, structure, query, calls)
				})
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"missing_method", map[string]any{"operation": map[string]any{}}, false},
		{"missing_operation", map[string]any{"method": "QUERY"}, false},
		{"wrong_method", map[string]any{"method": "POST", "operation": map[string]any{}}, false},
		{"envelope_type", true, false},
		{"operation_type", map[string]any{"method": "QUERY", "operation": true}, false},
		{"unknown_envelope_field", map[string]any{"method": "QUERY", "operation": map[string]any{}, "extra": true}, false},
		{"minimal", map[string]any{"method": "QUERY", "operation": map[string]any{}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := officialData(t, func(document map[string]any) {
				officialObject(officialObject(document["paths"])["/query"])["x-cratis-query"] = tc.value
			})
			officialAssertCase(t, data, tc.valid, structure, query, calls)
		})
	}
	t.Log("checked native/QUERY structural and QUERY-envelope cases against unmodified official operation/response definitions")
}

func TestOfficialCapabilitySchemaAdmissionAndCounts(t *testing.T) {
	structure, query, calls := officialHarness(t)
	for _, keyword := range officialCountKeywords {
		max := new(big.Int).SetUint64(uint64(^uint(0) >> 1))
		above := new(big.Int).Add(new(big.Int).Set(max), big.NewInt(1)).String()
		for _, tc := range []struct {
			name, value string
			valid       bool
		}{
			{"zero", "0", true}, {"max", max.String(), true}, {"above", above, false}, {"negative", "-1", false}, {"fraction", "1.5", false}, {"wrap", "18446744073709551616", false}, {"math_integer", "1.0", true},
		} {
			t.Run(keyword+"_"+tc.name, func(t *testing.T) {
				data := officialData(t, func(document map[string]any) {
					officialObject(officialSchemas(document)["Signed"])[keyword] = json.Number(tc.value)
				})
				officialAssertCase(t, data, tc.valid, structure, query, calls)
			})
		}
	}
	for _, tc := range []struct {
		keyword string
		value   any
	}{
		{"$id", "https://elsewhere.invalid/rebase"}, {"$id", "relative.json"}, {"$schema", "https://example.invalid/meta"}, {"$schema", "https://json-schema.org/draft/2019-09/schema"},
		{"$dynamicRef", "#meta"}, {"$dynamicAnchor", "meta"}, {"$anchor", "anchor"}, {"$vocabulary", map[string]any{"https://example.invalid/vocab": true}},
		{"contentEncoding", "unknown"}, {"contentMediaType", "application/json"}, {"contentSchema", true}, {"unknownAssertion", true}, {"discriminator", map[string]any{}}, {"nullable", true}, {"format", "media-range"}, {"format", "made-up"},
		{"type", "non-type"}, {"minimum", "1"}, {"multipleOf", json.Number("0")}, {"required", []any{"duplicate", "duplicate"}}, {"examples", "not-an-array"}, {"pattern", "["},
	} {
		t.Run(tc.keyword+"_"+fmt.Sprint(tc.value), func(t *testing.T) {
			data := officialData(t, func(document map[string]any) {
				officialObject(officialSchemas(document)["Signed"])[tc.keyword] = tc.value
			})
			officialAssertCase(t, data, false, structure, query, calls)
		})
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"nested_keyword", map[string]any{"properties": map[string]any{"x": map[string]any{"unknown": true}}}},
		{"unused_bad_schema", map[string]any{"type": "bad"}},
		{"schema_type", []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := officialData(t, func(document map[string]any) { officialSchemas(document)["Unused"] = tc.value })
			officialAssertCase(t, data, false, structure, query, calls)
		})
	}
	t.Logf("checked every count keyword (%d) before v6 Int64 conversion; min/max numeric extrema are unrestricted", len(officialCountKeywords))
}

func TestOfficialCapabilityDefaultsAndExamples(t *testing.T) {
	structure, query, calls := officialHarness(t)
	for _, schema := range []string{"Signed", "Unsigned"} {
		for _, keyword := range []string{"default", "example", "examples"} {
			for _, valid := range []bool{true, false} {
				t.Run(schema+"_"+keyword+"_"+strconv.FormatBool(valid), func(t *testing.T) {
					data := officialData(t, func(document map[string]any) {
						var value any = json.Number("9223372036854775807")
						if schema == "Unsigned" {
							value = json.Number("18446744073709551615")
						}
						if !valid {
							if schema == "Signed" {
								value = json.Number("9223372036854775808")
							} else {
								value = json.Number("18446744073709551616")
							}
						}
						if keyword == "examples" {
							value = []any{value}
						}
						officialObject(officialSchemas(document)[schema])[keyword] = value
					})
					officialAssertCase(t, data, valid, structure, query, calls)
				})
			}
		}
	}
	for _, isQuery := range []bool{false, true} {
		for _, named := range []bool{false, true} {
			for _, valid := range []bool{false, true} {
				t.Run(fmt.Sprintf("media_query_%t_named_%t_valid_%t", isQuery, named, valid), func(t *testing.T) {
					data := officialData(t, func(document map[string]any) {
						operation := officialOperationObject(document, isQuery)
						media := officialObject(officialObject(officialObject(officialObject(operation["responses"])["200"])["content"])["application/json"])
						value := json.Number("18446744073709551615")
						if !valid {
							value = "18446744073709551616"
						}
						if named {
							media["examples"] = map[string]any{"sample": map[string]any{"value": value}}
						} else {
							media["example"] = value
						}
					})
					officialAssertCase(t, data, valid, structure, query, calls)
				})
			}
		}
	}
	data := officialData(t, func(document map[string]any) {
		officialSchemas(document)["InstanceData"] = map[string]any{"type": "object", "default": map[string]any{"schema": map[string]any{"$id": "https://irrelevant.invalid", "$dynamicRef": "https://irrelevant.invalid"}, "$ref": "file:///irrelevant"}, "examples": []any{map[string]any{"schema": map[string]any{"madeUp": true}}}}
	})
	result := officialAssertCase(t, data, true, structure, query, calls)
	expected := slices.Clone(exactSchemaPointers)
	expected = append(expected, "/components/schemas/InstanceData")
	slices.Sort(expected)
	if !reflect.DeepEqual(result.inventory.schemas, expected) {
		t.Fatalf("instance data misclassified as schemas: %v", result.inventory.schemas)
	}
}

func TestOfficialCapabilityFormatsAndFiniteContent(t *testing.T) {
	structure, query, calls := officialHarness(t)
	data := officialData(t, func(document map[string]any) {
		officialSchemas(document)["Formatted"] = map[string]any{"type": "string", "format": "uuid", "default": "not-a-uuid"}
	})
	if _, err := officialCheck(data, structure, query, calls, false); err != nil {
		t.Fatalf("format annotation should not assert: %v", err)
	}
	officialAssertCase(t, data, false, structure, query, calls)
	data = officialData(t, func(document map[string]any) {
		officialSchemas(document)["Formatted"] = map[string]any{"type": "string", "format": "uuid", "default": "550e8400-e29b-41d4-a716-446655440000"}
	})
	officialAssertCase(t, data, true, structure, query, calls)
	for _, format := range officialAnnotationFormats {
		t.Run(format, func(t *testing.T) {
			data := officialData(t, func(document map[string]any) {
				officialSchemas(document)["Formatted"] = map[string]any{"type": "string", "format": format, "default": "annotation-not-codec-proof"}
			})
			officialAssertCase(t, data, true, structure, query, calls)
		})
	}
	// Known engine gap demonstrated, NOT used as a new acceptance authority.
	compiler := officialCompiler(calls, true)
	resource := map[string]any{"type": "string", "format": "media-range"}
	if err := compiler.AddResource("https://capability.invalid/unknown-format", resource); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://capability.invalid/unknown-format")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate("not a media range"); err != nil {
		t.Fatal("v6 unknown media-range witness changed")
	}
	for _, key := range append(slices.Clone(officialMediaKeys), "not a media range", "application/xml", "*/*") {
		t.Run(key, func(t *testing.T) {
			data := officialData(t, func(document map[string]any) {
				operation := officialOperationObject(document, true)
				response := officialObject(officialObject(operation["responses"])["200"])
				response["content"] = map[string]any{key: map[string]any{"schema": true}}
			})
			officialAssertCase(t, data, slices.Contains(officialMediaKeys, key), structure, query, calls)
		})
	}
	t.Log("uuid assertion differs from annotation; codec formats remain annotations; 4 finite renderer content keys, no generic media-range/precision proof")
}

func TestOfficialCapabilityTypedRefsSecurityAndParameters(t *testing.T) {
	structure, query, calls := officialHarness(t)
	for _, isQuery := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			mutate func(map[string]any, map[string]any)
			valid  bool
		}{
			{"local_response", func(document, op map[string]any) {
				officialObject(document["components"])["responses"] = map[string]any{"OK": map[string]any{"description": ""}}
				op["responses"] = map[string]any{"200": map[string]any{"$ref": "#/components/responses/OK"}}
			}, true},
			{"escaped_target", func(document, op map[string]any) {
				officialSchemas(document)["Escape"] = map[string]any{"type": "object", "properties": map[string]any{"a/b~c": map[string]any{"type": "string"}}}
				op["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Escape/properties/a~1b~0c"}}}}
			}, true},
			{"missing_response_target", func(document, op map[string]any) {
				op["responses"] = map[string]any{"200": map[string]any{"$ref": "#/components/responses/Missing"}}
			}, false},
			{"wrong_response_target", func(document, op map[string]any) {
				op["responses"] = map[string]any{"200": map[string]any{"$ref": "#/components/schemas/Signed"}}
			}, false},
			{"unknown_security", func(document, op map[string]any) { op["security"] = []any{map[string]any{"Missing": []any{}}} }, false},
			{"duplicate_opid", func(document, op map[string]any) {
				officialOperationObject(document, false)["operationId"] = "same"
				officialOperationObject(document, true)["operationId"] = "same"
			}, false},
			{"security_roles", func(document, op map[string]any) {
				officialObject(document["components"])["securitySchemes"] = map[string]any{"Key": map[string]any{"type": "apiKey", "in": "header", "name": "X-Key"}, "Alias": map[string]any{"$ref": "#/components/securitySchemes/Key"}}
				document["security"] = []any{map[string]any{"Alias": []any{"role"}}}
				op["security"] = []any{map[string]any{"Key": []any{"admin"}}, map[string]any{}}
			}, true},
			{"anonymous", func(document, op map[string]any) { op["security"] = []any{map[string]any{}} }, true},
			{"disable_security", func(document, op map[string]any) { op["security"] = []any{} }, true},
			{"security_bad_roles", func(document, op map[string]any) {
				officialObject(document["components"])["securitySchemes"] = map[string]any{"Key": map[string]any{"type": "http", "scheme": "bearer"}}
				op["security"] = []any{map[string]any{"Key": []any{true}}}
			}, false},
			{"duplicate_resolved_params", func(document, op map[string]any) {
				officialObject(document["components"])["parameters"] = map[string]any{"P": map[string]any{"name": "x", "in": "query", "schema": true}}
				op["parameters"] = []any{map[string]any{"$ref": "#/components/parameters/P"}, map[string]any{"name": "x", "in": "query", "schema": true}}
			}, false},
			{"different_param_location", func(document, op map[string]any) {
				op["parameters"] = []any{map[string]any{"name": "x", "in": "query", "schema": true}, map[string]any{"name": "x", "in": "header", "schema": true}}
			}, true},
			{"path_param_not_in_template", func(document, op map[string]any) {
				op["parameters"] = []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": true}}
			}, false},
		} {
			t.Run(fmt.Sprintf("query_%t_%s", isQuery, tc.name), func(t *testing.T) {
				data := officialData(t, func(document map[string]any) { tc.mutate(document, officialOperationObject(document, isQuery)) })
				officialAssertCase(t, data, tc.valid, structure, query, calls)
			})
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"unused_response_cycle", func(document map[string]any) {
			officialObject(document["components"])["responses"] = map[string]any{"A": map[string]any{"$ref": "#/components/responses/B"}, "B": map[string]any{"$ref": "#/components/responses/A"}}
		}, false},
		{"unused_security_cycle", func(document map[string]any) {
			officialObject(document["components"])["securitySchemes"] = map[string]any{"A": map[string]any{"$ref": "#/components/securitySchemes/A"}}
		}, false},
		{"unused_security_missing", func(document map[string]any) {
			officialObject(document["components"])["securitySchemes"] = map[string]any{"A": map[string]any{"$ref": "#/components/securitySchemes/Missing"}}
		}, false},
		{"unused_schema_missing", func(document map[string]any) {
			officialSchemas(document)["Unused"] = map[string]any{"$ref": "#/components/schemas/Missing"}
		}, false},
		{"schema_wrong_target", func(document map[string]any) { officialSchemas(document)["Unused"] = map[string]any{"$ref": "#/info"} }, false},
		{"invalid_pointer_escape", func(document map[string]any) {
			officialSchemas(document)["Unused"] = map[string]any{"$ref": "#/components/schemas/~2"}
		}, false},
		{"inherited_security_unknown", func(document map[string]any) { document["security"] = []any{map[string]any{"Unknown": []any{}}} }, false},
		{"template_missing", func(document map[string]any) {
			paths := officialObject(document["paths"])
			paths["/native/{id}"] = paths["/native"]
			delete(paths, "/native")
		}, false},
		{"template_parameter_override", func(document map[string]any) {
			paths := officialObject(document["paths"])
			item := officialObject(paths["/native"])
			item["parameters"] = []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": true}}
			officialOperationObject(document, false)["parameters"] = []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}}
			paths["/native/{id}"] = item
			delete(paths, "/native")
		}, true},
		{"query_template_override", func(document map[string]any) {
			paths := officialObject(document["paths"])
			item := officialObject(paths["/query"])
			item["parameters"] = []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": true}}
			officialOperationObject(document, true)["parameters"] = []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": true}}
			paths["/query/{id}"] = item
			delete(paths, "/query")
		}, true},
		{"duplicate_path_params", func(document map[string]any) {
			item := officialObject(officialObject(document["paths"])["/native"])
			p := map[string]any{"name": "x", "in": "query", "schema": true}
			item["parameters"] = []any{p, p}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := officialData(t, tc.mutate)
			officialAssertCase(t, data, tc.valid, structure, query, calls)
		})
	}
}

func TestOfficialCapabilityTwoExternalBoundaries(t *testing.T) {
	structure, query, calls := officialHarness(t)
	for _, ref := range []string{"https://outside.invalid/document.json#/x", "file:///tmp/outside.json#/x", "relative.json#/x", "//outside.invalid/document.json#/x", "#/missing", "#anchor"} {
		for _, site := range []string{"schema", "QUERY_schema", "response", "QUERY_response", "unused_component", "path_item", "operationRef"} {
			t.Run(site+"_"+ref, func(t *testing.T) {
				data := officialData(t, func(document map[string]any) {
					switch site {
					case "schema":
						officialSchemas(document)["Unused"] = map[string]any{"$ref": ref}
					case "QUERY_schema":
						operation := officialOperationObject(document, true)
						response := officialObject(officialObject(operation["responses"])["200"])
						media := officialObject(officialObject(response["content"])["application/json"])
						media["schema"] = map[string]any{"$ref": ref}
					case "response", "QUERY_response":
						operation := officialOperationObject(document, site == "QUERY_response")
						operation["responses"] = map[string]any{"200": map[string]any{"$ref": ref}}
					case "unused_component":
						officialObject(document["components"])["parameters"] = map[string]any{"Unused": map[string]any{"$ref": ref}}
					case "path_item":
						officialObject(document["paths"])["/unused"] = map[string]any{"$ref": ref}
					case "operationRef":
						response := officialObject(officialObject(officialOperationObject(document, true)["responses"])["200"])
						response["links"] = map[string]any{"Next": map[string]any{"operationRef": ref}}
					}
				})
				officialAssertCase(t, data, false, structure, query, calls)
			})
		}
	}
	// Independent compiler/metaschema boundary: not just instance-ref inspection.
	for _, tc := range []struct {
		name   string
		schema map[string]any
	}{
		{"external_schema", map[string]any{"$ref": "https://outside.invalid/schema"}},
		{"external_metaschema", map[string]any{"$schema": "https://outside.invalid/meta", "type": "string"}},
		{"file_schema", map[string]any{"$ref": "file:///tmp/schema.json"}},
		{"relative_schema", map[string]any{"$ref": "relative.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			denials := 0
			compiler := officialCompiler(&denials, true)
			if err := compiler.AddResource(exactResource, tc.schema); err != nil {
				t.Fatal(err)
			}
			if _, err := compiler.Compile(exactResource); err == nil {
				t.Fatal("STOP compiler accepted unavailable external resource")
			}
			if denials != 1 {
				t.Fatalf("expected exactly one rejecting loader call, got %d", denials)
			}
		})
	}
	t.Log("42 OpenAPI-instance reference cases refused before loader; 4 independent compiler/metaschema denials, no I/O implementation exists")
}

const officialContextFixture = `{
 "openapi":"3.1.1","jsonSchemaDialect":"https://json-schema.org/draft/2020-12/schema","info":{"title":"Context inventory","version":"1"},
 "components":{
  "schemas":{"All":{"type":"object","$defs":{"D":true},"properties":{"schema":true},"patternProperties":{"^a":true},"dependentSchemas":{"schema":true},"not":false,"if":true,"then":true,"else":false,"additionalProperties":true,"propertyNames":true,"unevaluatedProperties":true,"items":true,"contains":true,"unevaluatedItems":true,"allOf":[true],"anyOf":[true],"oneOf":[true],"prefixItems":[true]}},
  "parameters":{"P":{"name":"id","in":"path","required":true,"schema":true}},
  "headers":{"H":{"schema":true}},
  "requestBodies":{"B":{"content":{"application/json":{"schema":true}}}},
  "responses":{"R":{"description":"","headers":{"H":{"$ref":"#/components/headers/H"}},"content":{"application/json":{"schema":true,"examples":{"E":{"$ref":"#/components/examples/E"}}}}}},
  "examples":{"E":{"value":{"schema":{"$ref":"https://instance-data.invalid","unknown":true}}}},
  "callbacks":{"C":{"{$request.body#/url}":{"post":{"operationId":"callback","requestBody":{"content":{"application/json":{"schema":true}}}}}}},
  "pathItems":{"PI":{"get":{"operationId":"unused","parameters":[{"name":"q","in":"query","schema":true}]}}},
  "securitySchemes":{"OAuth":{"type":"oauth2","flows":{"implicit":{"authorizationUrl":"https://example.invalid/auth","scopes":{"read":"Read"}}}}}
 },
 "security":[{"OAuth":["read"]}],
 "paths":{"/p/{id}":{
  "parameters":[{"$ref":"#/components/parameters/P"}],
  "get":{"operationId":"ordinary","parameters":[{"name":"q","in":"query","schema":true}],"requestBody":{"$ref":"#/components/requestBodies/B"},"responses":{"200":{"$ref":"#/components/responses/R"}},"callbacks":{"C":{"$ref":"#/components/callbacks/C"}}},
  "x-cratis-query":{"method":"QUERY","operation":{"operationId":"query","parameters":[{"name":"X","in":"header","schema":true}],"responses":{"200":{"description":"","headers":{"H":{"schema":true}},"links":{"self":{"operationRef":"#/paths/~1p~1%7Bid%7D/get"}},"content":{"application/json":{"schema":true,"encoding":{"field":{"headers":{"H":{"schema":true}}}}}}}}}}
 }},
 "webhooks":{"event":{"post":{"operationId":"webhook","responses":{"200":{"description":"","content":{"application/json":{"schema":true}}}}}}}
}`

func TestOfficialCapabilityCompleteContextInventory(t *testing.T) {
	structure, query, calls := officialHarness(t)
	data := []byte(officialContextFixture)
	result := officialAssertCase(t, data, true, structure, query, calls)
	expected := []string{
		"/components/schemas/All", "/components/schemas/All/$defs/D", "/components/schemas/All/properties/schema", "/components/schemas/All/patternProperties/^a", "/components/schemas/All/dependentSchemas/schema",
		"/components/schemas/All/not", "/components/schemas/All/if", "/components/schemas/All/then", "/components/schemas/All/else", "/components/schemas/All/additionalProperties", "/components/schemas/All/propertyNames", "/components/schemas/All/unevaluatedProperties", "/components/schemas/All/items", "/components/schemas/All/contains", "/components/schemas/All/unevaluatedItems", "/components/schemas/All/allOf/0", "/components/schemas/All/anyOf/0", "/components/schemas/All/oneOf/0", "/components/schemas/All/prefixItems/0",
		"/components/parameters/P/schema", "/components/headers/H/schema", "/components/requestBodies/B/content/application~1json/schema", "/components/responses/R/content/application~1json/schema",
		"/components/callbacks/C/{$request.body#~1url}/post/requestBody/content/application~1json/schema", "/components/pathItems/PI/get/parameters/0/schema",
		"/paths/~1p~1{id}/get/parameters/0/schema", "/paths/~1p~1{id}/x-cratis-query/operation/parameters/0/schema", "/paths/~1p~1{id}/x-cratis-query/operation/responses/200/headers/H/schema", "/paths/~1p~1{id}/x-cratis-query/operation/responses/200/content/application~1json/schema", "/paths/~1p~1{id}/x-cratis-query/operation/responses/200/content/application~1json/encoding/field/headers/H/schema",
		"/webhooks/event/post/responses/200/content/application~1json/schema",
	}
	slices.Sort(expected)
	if !reflect.DeepEqual(result.inventory.schemas, expected) {
		t.Fatalf("STOP complete contextual inventory gap: got %v want %v", result.inventory.schemas, expected)
	}
	for _, pointer := range expected {
		location, err := url.Parse(result.compiled[pointer].Location)
		if err != nil || location.Fragment != pointer || strings.Split(location.String(), "#")[0] != exactResource {
			t.Fatalf("original pointer changed: %s -> %s", pointer, result.compiled[pointer].Location)
		}
	}
	if len(result.inventory.operations) != 5 {
		t.Fatalf("operation inventory = %d want 5", len(result.inventory.operations))
	}
	t.Logf("compiled complete expected contextual set: %d schema pointers and 5 operation pointers (unused, callback, webhook, native, QUERY); instance-data refs ignored", len(expected))
}

func TestOfficialCapabilityAdmittedAssertions(t *testing.T) {
	structure, query, calls := officialHarness(t)
	for _, tc := range []struct{ name, schema, valid, invalid string }{
		{"length_pattern", `{"type":"string","minLength":2,"maxLength":3,"pattern":"^a"}`, `"ab"`, `"zz"`},
		{"length_short", `{"type":"string","minLength":2}`, `"ab"`, `"a"`},
		{"length_long", `{"type":"string","maxLength":2}`, `"ab"`, `"abc"`},
		{"items_min", `{"type":"array","minItems":1}`, `[1]`, `[]`},
		{"items_max", `{"type":"array","maxItems":1}`, `[1]`, `[1,2]`},
		{"unique", `{"type":"array","uniqueItems":true}`, `[1,2]`, `[1,1]`},
		{"items", `{"type":"array","items":{"type":"integer"}}`, `[1]`, `["x"]`},
		{"prefix_unevaluated", `{"type":"array","prefixItems":[{"type":"string"}],"unevaluatedItems":false}`, `["a"]`, `["a",1]`},
		{"contains_min", `{"type":"array","contains":{"type":"integer"},"minContains":2}`, `[1,2]`, `[1,"a"]`},
		{"contains_max", `{"type":"array","contains":{"type":"integer"},"minContains":0,"maxContains":1}`, `[1,"a"]`, `[1,2]`},
		{"properties_min", `{"type":"object","minProperties":1}`, `{"a":1}`, `{}`},
		{"properties_max", `{"type":"object","maxProperties":1}`, `{"a":1}`, `{"a":1,"b":2}`},
		{"property_names", `{"type":"object","propertyNames":{"pattern":"^a"}}`, `{"abc":1}`, `{"b":1}`},
		{"pattern_properties", `{"type":"object","patternProperties":{"^a":{"type":"integer"}}}`, `{"a":1}`, `{"a":"x"}`},
		{"additional", `{"type":"object","properties":{"a":true},"additionalProperties":false}`, `{"a":1}`, `{"b":1}`},
		{"unevaluated", `{"type":"object","allOf":[{"properties":{"a":true}}],"unevaluatedProperties":false}`, `{"a":1}`, `{"b":1}`},
		{"dependent_required", `{"type":"object","dependentRequired":{"a":["b"]}}`, `{"a":1,"b":2}`, `{"a":1}`},
		{"dependent_schema", `{"type":"object","dependentSchemas":{"a":{"required":["b"]}}}`, `{"a":1,"b":2}`, `{"a":1}`},
		{"all_of", `{"allOf":[{"type":"integer"},{"minimum":1}]}`, `1`, `0`},
		{"any_of", `{"anyOf":[{"type":"integer"},{"type":"null"}]}`, `null`, `true`},
		{"one_of", `{"oneOf":[{"type":"integer"},{"minimum":0}]}`, `-1`, `1`},
		{"not", `{"not":{"type":"null"}}`, `true`, `null`},
		{"conditional", `{"if":{"type":"integer"},"then":{"minimum":0},"else":{"type":"string"}}`, `1`, `-1`},
		{"conditional_else", `{"if":{"type":"integer"},"then":{"minimum":0},"else":{"type":"string"}}`, `"a"`, `true`},
		{"boolean", `false`, `null`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.schema))
			if err != nil {
				t.Fatal(err)
			}
			data := officialData(t, func(document map[string]any) { officialSchemas(document)["Assertion"] = schema })
			result := officialAssertCase(t, data, true, structure, query, calls)
			compiled := result.compiled["/components/schemas/Assertion"]
			for index, text := range []string{tc.valid, tc.invalid} {
				value, err := jsonschema.UnmarshalJSON(strings.NewReader(text))
				if err != nil {
					t.Fatal(err)
				}
				err = compiled.Validate(value)
				want := index == 0 && tc.name != "boolean"
				if (err == nil) != want {
					t.Fatalf("STOP assertion %s instance %s: %v", tc.name, text, err)
				}
			}
		})
	}
	t.Log("checked 25 assertion schemas with paired exact instances, including schema-valued nested positions")
}

func TestOfficialCapabilityExactDTOOwnership(t *testing.T) {
	structure, query, calls := officialHarness(t)
	input := officialData(t, nil)
	result := officialAssertCase(t, input, true, structure, query, calls)
	first, err := result.document.bytes()
	if err != nil {
		t.Fatal(err)
	}
	input[0] = '!'
	second, err := result.document.bytes()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("DTO borrowed input bytes")
	}
	first[0] = '!'
	third, err := result.document.bytes()
	if err != nil || !bytes.Equal(second, third) {
		t.Fatal("DTO returned shared bytes")
	}
	decoded, err := decodeExactDocument(third)
	if err != nil {
		t.Fatal(err)
	}
	fourth, err := decoded.bytes()
	if err != nil || !bytes.Equal(third, fourth) || !reflect.DeepEqual(decoded.value, result.document.value) {
		t.Fatal("DTO roundtrip is not exact/deterministic")
	}
	for _, data := range [][]byte{append(bytes.Clone(third), []byte(` {}`)...), []byte(`{"openapi":`), []byte(`[]`)} {
		if _, err := decodeExactDocument(data); err == nil {
			t.Fatal("malformed/non-document DTO accepted")
		}
	}
	t.Log("copy-owned DTO and fresh bytes; exact deterministic roundtrip; 3 invalid DTO controls")
}
