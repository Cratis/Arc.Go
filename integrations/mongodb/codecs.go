// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/cratis/fundamentals.go/concepts"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// NewRegistry validates the supplied type graphs without calling application
// codecs, ConceptValue methods, or constructors. It returns an independent,
// caller-owned driver registry. Finish all registration before concurrent use;
// do not mutate a registry after installing it on a driver handle.
// Persisted model fields require explicit JSON names; optional BSON names are
// storage overrides. Unsupported mappings return ErrUnsupportedModel.
// Decoding a registered value is failure-atomic, including nested destinations.
func NewRegistry(types ...reflect.Type) (*bson.Registry, error) {
	registry, _, err := newRegistry(types...)
	return registry, err
}

func newRegistry(types ...reflect.Type) (*bson.Registry, map[reflect.Type]*codec, error) {
	if len(types) == 0 {
		return nil, nil, ErrUnsupportedModel
	}
	d := discovery{map[reflect.Type]*codec{}, map[reflect.Type]bool{}}
	for _, t := range types {
		if _, err := d.discover(t, 0); err != nil {
			return nil, nil, err
		}
	}
	r := bson.NewRegistry()
	for t, c := range d.codecs {
		r.RegisterTypeEncoder(t, c)
		r.RegisterTypeDecoder(t, c)
	}
	return r, d.codecs, nil
}

// EncodeValue implements the driver's codec contract.
func (c *codec) EncodeValue(_ bson.EncodeContext, writer bson.ValueWriter, value reflect.Value) error {
	if !value.IsValid() || value.Type() != c.typeOf {
		return ErrValue
	}
	if err := c.encode(writer, value); err != nil {
		return ErrValue
	}
	return nil
}

// DecodeValue implements the driver's codec contract with a fresh destination.
func (c *codec) DecodeValue(_ bson.DecodeContext, reader bson.ValueReader, value reflect.Value) error {
	if !value.IsValid() || !value.CanSet() || value.Type() != c.typeOf {
		return ErrValue
	}
	temporary := reflect.New(c.typeOf).Elem()
	if err := c.decode(reader, temporary); err != nil {
		return ErrValue
	}
	value.Set(temporary)
	return nil
}

func (c *codec) encode(w bson.ValueWriter, v reflect.Value) error {
	if c.typeOf.Kind() == reflect.Pointer {
		if v.IsNil() {
			return w.WriteNull()
		}
		return c.element.encode(w, v.Elem())
	}
	if c.representation.Type != nil {
		data, err := json.Marshal(v.Interface())
		if err != nil || concepts.CheckJSON(c.representation, data) != nil {
			return ErrValue
		}
		underlying := reflect.New(c.representation.Type)
		if err := json.Unmarshal(data, underlying.Interface()); err != nil {
			return ErrValue
		}
		return encodeScalar(w, underlying.Elem())
	}
	if c.typeOf.Kind() == reflect.Slice {
		array, err := w.WriteArray()
		if err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			element, err := array.WriteArrayElement()
			if err != nil {
				return err
			}
			if err := c.element.encode(element, v.Index(i)); err != nil {
				return err
			}
		}
		return array.WriteArrayEnd()
	}
	if len(c.fields) == 0 {
		return encodeScalar(w, v)
	}
	document, err := w.WriteDocument()
	if err != nil {
		return err
	}
	for _, f := range c.fields {
		value := v.Field(f.index)
		if f.omit && emptyValue(value) {
			continue
		}
		if f.name == "_id" && value.Kind() == reflect.Pointer && value.IsNil() {
			return ErrValue
		}
		element, err := document.WriteDocumentElement(f.name)
		if err != nil {
			return err
		}
		if err := f.codec.encode(element, value); err != nil {
			return err
		}
	}
	return document.WriteDocumentEnd()
}

func emptyValue(v reflect.Value) bool {
	if v.Kind() == reflect.Slice || v.Kind() == reflect.String {
		return v.Len() == 0
	}
	return v.IsZero()
}

func (c *codec) decode(r bson.ValueReader, v reflect.Value) error {
	if r.Type() == bson.TypeNull {
		if c.typeOf.Kind() != reflect.Pointer && c.typeOf.Kind() != reflect.Slice {
			return ErrValue
		}
		if err := r.ReadNull(); err != nil {
			return err
		}
		if c.typeOf.Kind() == reflect.Slice {
			v.Set(reflect.MakeSlice(c.typeOf, 0, 0))
		}
		return nil
	}
	if c.typeOf.Kind() == reflect.Pointer {
		v.Set(reflect.New(c.typeOf.Elem()))
		return c.element.decode(r, v.Elem())
	}
	if c.representation.Type != nil {
		if r.Type() == bson.TypeEmbeddedDocument {
			document, err := r.ReadDocument()
			if err != nil {
				return err
			}
			key, element, err := document.ReadElement()
			if err != nil || (key != "Value" && key != "value") {
				return ErrValue
			}
			if err := c.decodeConcept(element, v); err != nil {
				return err
			}
			if _, _, err := document.ReadElement(); !errors.Is(err, bson.ErrEOD) {
				return ErrValue
			}
			return nil
		}
		return c.decodeConcept(r, v)
	}
	if c.typeOf.Kind() == reflect.Slice {
		array, err := r.ReadArray()
		if err != nil {
			return ErrValue
		}
		v.Set(reflect.MakeSlice(c.typeOf, 0, 0))
		for {
			element, err := array.ReadValue()
			if errors.Is(err, bson.ErrEOA) {
				return nil
			}
			if err != nil {
				return err
			}
			value := reflect.New(c.typeOf.Elem()).Elem()
			if err := c.element.decode(element, value); err != nil {
				return err
			}
			v.Set(reflect.Append(v, value))
		}
	}
	if len(c.fields) == 0 {
		return decodeScalar(r, v)
	}
	document, err := r.ReadDocument()
	if err != nil {
		return ErrValue
	}
	seen := make(map[string]bool)
	for _, f := range c.fields {
		if f.codec.typeOf.Kind() == reflect.Slice {
			v.Field(f.index).Set(reflect.MakeSlice(f.codec.typeOf, 0, 0))
		}
	}
	for {
		key, element, err := document.ReadElement()
		if errors.Is(err, bson.ErrEOD) {
			break
		}
		if err != nil {
			return err
		}
		if seen[key] {
			return ErrValue
		}
		seen[key] = true
		matched := false
		for _, f := range c.fields {
			if f.name != key {
				continue
			}
			matched = true
			if key == "_id" && element.Type() == bson.TypeNull {
				return ErrValue
			}
			if err := f.codec.decode(element, v.Field(f.index)); err != nil {
				return err
			}
			break
		}
		if !matched {
			if err := element.Skip(); err != nil {
				return err
			}
		}
	}
	for _, f := range c.fields {
		if f.name == "_id" && !seen["_id"] {
			return ErrValue
		}
	}
	return nil
}

func (c *codec) decodeConcept(r bson.ValueReader, v reflect.Value) error {
	underlying := reflect.New(c.representation.Type).Elem()
	if err := decodeScalar(r, underlying); err != nil {
		return err
	}
	data, err := json.Marshal(underlying.Interface())
	if err != nil || concepts.CheckJSON(c.representation, data) != nil {
		return ErrValue
	}
	if err := json.Unmarshal(data, v.Addr().Interface()); err != nil {
		return ErrValue
	}
	return nil
}

func encodeScalar(w bson.ValueWriter, v reflect.Value) error {
	switch value := v.Interface().(type) {
	case concepts.UUID:
		return w.WriteBinaryWithSubtype(value[:], 4)
	case concepts.DateOnly:
		year, month, day := value.Date()
		return w.WriteDateTime(time.Date(year, month, day, 12, 0, 0, 0, time.UTC).UnixMilli())
	case concepts.TimeOnly:
		return w.WriteDateTime(value.Ticks() / 10_000)
	case concepts.TimeSpan:
		return w.WriteString(value.String())
	case time.Time:
		millis := value.UnixMilli()
		// Reject overflow instead of wrapping a time beyond BSON's int64 range.
		if !time.UnixMilli(millis).Equal(value.Truncate(time.Millisecond)) {
			return ErrValue
		}
		return w.WriteDateTime(millis)
	}
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return ErrValue
		}
		return w.WriteString(v.String())
	case reflect.Bool:
		return w.WriteBoolean(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		value := v.Int()
		if value < math.MinInt32 || value > math.MaxInt32 {
			return ErrValue
		}
		return w.WriteInt32(int32(value))
	case reflect.Int64:
		return w.WriteInt64(v.Int())
	case reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return w.WriteInt64(int64(v.Uint()))
	case reflect.Uint, reflect.Uint64:
		value, err := bson.ParseDecimal128(strconv.FormatUint(v.Uint(), 10))
		if err != nil {
			return ErrValue
		}
		return w.WriteDecimal128(value)
	case reflect.Float32, reflect.Float64:
		value := v.Float()
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return ErrValue
		}
		return w.WriteDouble(value)
	}
	return ErrValue
}

func decodeScalar(r bson.ValueReader, v reflect.Value) error {
	switch v.Type() {
	case reflect.TypeFor[concepts.UUID]():
		var id concepts.UUID
		if r.Type() == bson.TypeString {
			text, err := r.ReadString()
			if err != nil {
				return ErrValue
			}
			var parseErr error
			id, parseErr = concepts.ParseUUID(text)
			if parseErr != nil {
				return ErrValue
			}
		} else {
			data, subtype, err := r.ReadBinary()
			if err != nil || subtype != 4 || len(data) != 16 {
				return ErrValue
			}
			copy(id[:], data)
		}
		v.Set(reflect.ValueOf(id))
		return nil
	case reflect.TypeFor[time.Time](), reflect.TypeFor[concepts.DateOnly](), reflect.TypeFor[concepts.TimeOnly]():
		millis, err := r.ReadDateTime()
		if err != nil {
			return ErrValue
		}
		value := time.UnixMilli(millis).UTC()
		switch v.Type() {
		case reflect.TypeFor[time.Time]():
			v.Set(reflect.ValueOf(value))
		case reflect.TypeFor[concepts.DateOnly]():
			date, err := concepts.NewDateOnly(value.Year(), value.Month(), value.Day())
			if err != nil {
				return ErrValue
			}
			v.Set(reflect.ValueOf(date))
		case reflect.TypeFor[concepts.TimeOnly]():
			ticks := int64(value.Hour()*3600+value.Minute()*60+value.Second())*10_000_000 + int64(value.Nanosecond()/100)
			clock, err := concepts.NewTimeOnly(ticks)
			if err != nil {
				return ErrValue
			}
			v.Set(reflect.ValueOf(clock))
		}
		return nil
	case reflect.TypeFor[concepts.TimeSpan]():
		text, err := r.ReadString()
		if err != nil {
			return ErrValue
		}
		value, err := concepts.ParseTimeSpan(text)
		if err != nil {
			return ErrValue
		}
		v.Set(reflect.ValueOf(value))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		value, err := r.ReadString()
		if err != nil || !utf8.ValidString(value) {
			return ErrValue
		}
		v.SetString(value)
		return nil
	case reflect.Bool:
		value, err := r.ReadBoolean()
		if err != nil {
			return ErrValue
		}
		v.SetBool(value)
		return nil
	default:
		return decodeNumber(r, v)
	}
}
