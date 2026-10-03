// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/serialization"
	fconcepts "github.com/cratis/fundamentals.go/concepts"
)

// RegisterSchema installs a copied JSON schema override for an exact Go type.
// Overrides must be schema objects or booleans. They do not run application codecs.
func RegisterSchema[T any](b *Builder, document json.RawMessage) error {
	if b == nil {
		return ErrInvalidOptions
	}
	if b.attempted {
		return ErrFrozen
	}
	var value any
	if err := json.Unmarshal(document, &value); err != nil {
		return err
	}
	if err := validateSchema(value, 0); err != nil {
		return err
	}
	typ := reflect.TypeFor[T]()
	if b.schemas == nil {
		b.schemas = make(map[reflect.Type]json.RawMessage)
	}
	if _, exists := b.schemas[typ]; exists {
		return ErrInvalidOptions
	}
	b.schemas[typ] = append(json.RawMessage(nil), document...)
	return nil
}
func validateSchema(value any, depth int) error {
	if depth > 64 {
		return ErrSchemaUnavailable
	}
	if _, ok := value.(bool); ok {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return ErrSchemaUnavailable
	}
	for key, v := range object {
		switch key {
		case "type":
			allowed := func(s any) bool {
				text, ok := s.(string)
				return ok && strings.Contains("|object|array|string|number|integer|boolean|null|", "|"+text+"|")
			}
			if values, ok := v.([]any); ok {
				if len(values) == 0 {
					return ErrSchemaUnavailable
				}
				for _, s := range values {
					if !allowed(s) {
						return ErrSchemaUnavailable
					}
				}
			} else if !allowed(v) {
				return ErrSchemaUnavailable
			}
		case "properties", "$defs", "definitions":
			entries, ok := v.(map[string]any)
			if !ok {
				return ErrSchemaUnavailable
			}
			for _, s := range entries {
				if err := validateSchema(s, depth+1); err != nil {
					return err
				}
			}
		case "items", "additionalProperties", "not", "if", "then", "else":
			if err := validateSchema(v, depth+1); err != nil {
				return err
			}
		case "allOf", "anyOf", "oneOf":
			entries, ok := v.([]any)
			if !ok || len(entries) == 0 {
				return ErrSchemaUnavailable
			}
			for _, s := range entries {
				if err := validateSchema(s, depth+1); err != nil {
					return err
				}
			}
		case "required":
			entries, ok := v.([]any)
			if !ok {
				return ErrSchemaUnavailable
			}
			for _, name := range entries {
				if _, ok := name.(string); !ok {
					return ErrSchemaUnavailable
				}
			}
		case "$ref":
			if _, ok := v.(string); !ok {
				return ErrSchemaUnavailable
			}
		}
	}
	return nil
}
func (a *Application) schema(t reflect.Type) (any, error) {
	return a.schemaType(t, make(map[reflect.Type]bool), 0)
}
func optionalType(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() == reflect.Struct && t.PkgPath() == reflect.TypeFor[serialization.Optional[int]]().PkgPath() && strings.HasPrefix(t.Name(), "Optional[") {
		return t.Field(0).Type, true
	}
	return nil, false
}
func (a *Application) schemaType(t reflect.Type, seen map[reflect.Type]bool, depth int) (any, error) {
	if document, ok := a.schemas[t]; ok {
		return json.RawMessage(append([]byte(nil), document...)), nil
	}
	fail := func() (any, error) {
		return nil, fmt.Errorf("%w: type %v; register an explicit schema", ErrSchemaUnavailable, t)
	}
	if depth >= modelshape.MaxDepth || seen[t] {
		return fail()
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return map[string]any{}, nil
	}
	if element, ok := optionalType(t); ok {
		s, err := a.schemaType(element, seen, depth+1)
		return nullable(s), err
	}
	if t.Kind() == reflect.Pointer {
		s, err := a.schemaType(t.Elem(), seen, depth+1)
		return nullable(s), err
	}
	if representation, ok, err := fconcepts.Underlying(t); err != nil {
		return nil, err
	} else if ok {
		scalar := representation.Type
		switch scalar {
		case reflect.TypeFor[concepts.UUID]():
			return map[string]any{"type": "string", "format": "uuid"}, nil
		case reflect.TypeFor[concepts.DateOnly]():
			return map[string]any{"type": "string", "format": "date"}, nil
		case reflect.TypeFor[concepts.TimeOnly]():
			return map[string]any{"type": "string", "format": "time"}, nil
		case reflect.TypeFor[concepts.TimeSpan]():
			return map[string]any{"type": "string", "description": "Invariant .NET duration"}, nil
		}
		if scalar != t {
			return a.schemaType(scalar, seen, depth+1)
		}
	}
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	for _, codec := range []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](), reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler]()} {
		if t.Implements(codec) || reflect.PointerTo(t).Implements(codec) {
			return fail()
		}
	}
	seen[t] = true
	defer delete(seen, t)
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Array, reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}, nil
		}
		items, err := a.schemaType(t.Elem(), seen, depth+1)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fail()
		}
		items, err := a.schemaType(t.Elem(), seen, depth+1)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": items}, nil
	case reflect.Struct:
		fields, err := modelshape.Fields(t)
		if err != nil {
			return nil, err
		}
		properties := map[string]any{}
		required := []string{}
		for _, f := range fields {
			s, err := a.schemaType(f.Type, seen, depth+1)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", f.Name, err)
			}
			properties[f.Name] = s
			_, optional := optionalType(f.Type)
			if f.Type.Kind() != reflect.Pointer && !optional && !f.OmitEmpty && !f.OmitZero {
				required = append(required, f.Name)
			}
		}
		return map[string]any{"type": "object", "properties": properties, "required": required}, nil
	default:
		return fail()
	}
}
func nullable(schema any) any {
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}
