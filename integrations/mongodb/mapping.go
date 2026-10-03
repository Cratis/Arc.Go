// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type field struct {
	index    int
	jsonName string
	name     string
	omit     bool
	codec    *codec
}

type codec struct {
	typeOf         reflect.Type
	representation concepts.Representation
	fields         []field
	element        *codec
	height         int // Longest downward path, independent of discovery order.
}

type discovery struct {
	codecs map[reflect.Type]*codec
	active map[reflect.Type]bool
}

func (d *discovery) discover(t reflect.Type, depth int) (*codec, error) {
	if t == nil || depth > 64 || d.active[t] || hasBSONCodec(t) {
		return nil, ErrUnsupportedModel
	}
	if c := d.codecs[t]; c != nil {
		if depth+c.height > 64 {
			return nil, ErrUnsupportedModel
		}
		return c, nil
	}
	if len(d.codecs)+len(d.active) >= 256 {
		return nil, ErrUnsupportedModel
	}
	d.active[t] = true
	defer delete(d.active, t)
	c := &codec{typeOf: t}
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		var err error
		c.element, err = d.discover(t.Elem(), depth+1)
		if err != nil {
			return nil, err
		}
	} else {
		r, recognized, err := concepts.Underlying(t)
		if err != nil {
			return nil, ErrUnsupportedModel
		}
		if recognized {
			c.representation = r
		} else if t != reflect.TypeFor[time.Time]() {
			if hasCustomCodec(t) {
				return nil, ErrUnsupportedModel
			}
			switch t.Kind() {
			case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
				reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64:
			case reflect.Struct:
				if t.Name() == "" || t.NumField() == 0 {
					return nil, ErrUnsupportedModel
				}
				jsonNames, bsonNames := map[string]bool{}, map[string]bool{}
				for i := 0; i < t.NumField(); i++ {
					f := t.Field(i)
					if !f.IsExported() || f.Anonymous {
						return nil, ErrUnsupportedModel
					}
					jsonName, jsonOmit, ok := fieldTag(f.Tag.Get("json"))
					if !ok {
						return nil, ErrUnsupportedModel
					}
					name, omit := jsonName, jsonOmit
					if tag, exists := f.Tag.Lookup("bson"); exists {
						name, omit, ok = fieldTag(tag)
						if !ok {
							return nil, ErrUnsupportedModel
						}
					}
					if jsonNames[jsonName] || bsonNames[name] || (name == "_id" && (omit || jsonOmit)) {
						return nil, ErrUnsupportedModel
					}
					jsonNames[jsonName], bsonNames[name] = true, true
					fc, err := d.discover(f.Type, depth+1)
					if err != nil {
						return nil, err
					}
					c.fields = append(c.fields, field{i, jsonName, name, omit, fc})
				}
			default:
				return nil, ErrUnsupportedModel
			}
		}
	}
	if c.element != nil {
		c.height = c.element.height + 1
	}
	for _, field := range c.fields {
		c.height = max(c.height, field.codec.height+1)
	}
	d.codecs[t] = c
	return c, nil
}

func fieldTag(tag string) (string, bool, bool) {
	parts := strings.Split(tag, ",")
	if !validField(parts[0]) || parts[0] == "-" {
		return "", false, false
	}
	omit := false
	for _, option := range parts[1:] {
		if option != "omitempty" || omit {
			return "", false, false
		}
		omit = true
	}
	return parts[0], omit, true
}

// BSON document hooks can bypass the driver's registry entirely. Check every
// type and pointer method set before container/concept/cache fast paths. Required
// JSON/text concept conversion remains supported through the declared codecs.
func hasBSONCodec(t reflect.Type) bool {
	for _, contract := range []reflect.Type{
		reflect.TypeFor[bson.Marshaler](), reflect.TypeFor[bson.Unmarshaler](),
		reflect.TypeFor[bson.ValueMarshaler](), reflect.TypeFor[bson.ValueUnmarshaler](),
	} {
		if t.Implements(contract) || reflect.PointerTo(t).Implements(contract) {
			return true
		}
	}
	return false
}

func hasCustomCodec(t reflect.Type) bool {
	for _, contract := range []reflect.Type{
		reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](),
		reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler](),
		reflect.TypeFor[bson.Marshaler](), reflect.TypeFor[bson.Unmarshaler](),
		reflect.TypeFor[bson.ValueMarshaler](), reflect.TypeFor[bson.ValueUnmarshaler](),
	} {
		if t.Implements(contract) || reflect.PointerTo(t).Implements(contract) {
			return true
		}
	}
	return false
}

func (c *codec) scalar() bool {
	return c.element == nil && len(c.fields) == 0
}
