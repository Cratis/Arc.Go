// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	fconcepts "github.com/cratis/fundamentals.go/concepts"
)

type conceptInfo struct {
	once           sync.Once
	representation fconcepts.Representation
	recognized     bool
	err            error
}

var conceptTypes sync.Map // reflect.Type -> *conceptInfo

// conceptType inspects each exact type once, including unsuccessful recognition.
// Discovery is metadata-only: it never constructs values or executes user code.
func conceptType(t reflect.Type) (fconcepts.Representation, bool, error) {
	entry, loaded := conceptTypes.Load(t)
	if !loaded {
		entry, _ = conceptTypes.LoadOrStore(t, &conceptInfo{})
	}
	info := entry.(*conceptInfo)
	info.once.Do(func() {
		info.representation, info.recognized, info.err = fconcepts.Underlying(t)
	})
	return info.representation, info.recognized, info.err
}

type typePlan struct {
	once sync.Once
	err  error
}

var typePlans sync.Map // reflect.Type -> *typePlan

// ValidateType prepares Arc's field plans and rejects invalid concept declarations
// in a model, its pointers, collections and exported JSON fields. Call it when
// registering a model to fail before serving requests. Marshal and Unmarshal also
// call it before using a type. Errors preserve Fundamentals.Go's ErrInvalidConcept
// and *concepts.TypeError for errors.Is/As. Calls are concurrency-safe and cached
// per exact type, including errors. No application values or codecs are executed.
// Custom non-concept codecs are opaque; interfaces are checked when their concrete
// values are encoded. This does not validate codec output or domain invariants.
func ValidateType(t reflect.Type) error {
	entry, loaded := typePlans.Load(t)
	if !loaded {
		entry, _ = typePlans.LoadOrStore(t, &typePlan{})
	}
	plan := entry.(*typePlan)
	plan.once.Do(func() {
		plan.err = validateType(t, make(map[reflect.Type]bool))
	})
	return plan.err
}

func validateType(t reflect.Type, seen map[reflect.Type]bool) error {
	if seen[t] {
		return nil
	}
	seen[t] = true
	_, recognized, err := conceptType(t)
	if err != nil || recognized {
		return err
	}
	// Optional owns presence, but delegates its element's encoding to Arc.
	if t.Kind() == reflect.Struct && t.Implements(reflect.TypeFor[interface{ optionalValue() (any, bool, bool) }]()) {
		return validateType(t.Field(0).Type, seen)
	}
	if hasCodec(t) {
		return nil
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return validateType(t.Elem(), seen)
	case reflect.Map:
		if err := validateType(t.Key(), seen); err != nil {
			return fmt.Errorf("dictionary key: %w", err)
		}
		return validateType(t.Elem(), seen)
	case reflect.Struct:
		members, err := fields(t)
		if err != nil {
			return err
		}
		for _, member := range members {
			if err := validateType(t.FieldByIndex(member.index).Type, seen); err != nil {
				return fmt.Errorf("field %s: %w", member.name, err)
			}
		}
	}
	return nil
}

func hasCodec(t reflect.Type) bool {
	for _, codec := range []reflect.Type{
		reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](),
		reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler](),
	} {
		if t.Implements(codec) || reflect.PointerTo(t).Implements(codec) {
			return true
		}
	}
	return false
}
