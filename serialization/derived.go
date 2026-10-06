// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/cratis/arc.go/concepts"
)

// DerivedDeclaration explicitly associates a concrete struct (or its pointer)
// with a named interface wire contract and a canonical UUID discriminator.
// Go embedding alone is not inheritance. Codecs on derived structs are unsupported.
type DerivedDeclaration struct {
	// Default selects the concrete base representation when no discriminator is supplied.
	// Its ID must be empty; explicit unknown IDs still fail closed.
	Default bool
	ID      string
	Base    reflect.Type
	Type    reflect.Type
}

var derivedRegistry = struct {
	sync.RWMutex
	byType   map[reflect.Type]DerivedDeclaration
	byID     map[string]DerivedDeclaration
	bases    map[reflect.Type]bool
	defaults map[reflect.Type]DerivedDeclaration
}{byType: map[reflect.Type]DerivedDeclaration{}, byID: map[string]DerivedDeclaration{}, bases: map[reflect.Type]bool{}, defaults: map[reflect.Type]DerivedDeclaration{}}

// RegisterDerivedTypes installs process-wide wire declarations, like the frontend
// derived-type registry. Call during composition, before handling requests. The
// complete batch is validated before publication. Exact repeated declarations
// are idempotent; conflicting IDs/types fail without changing the registry.
// Calls and subsequent serialization are concurrency-safe. No codecs are executed.
func RegisterDerivedTypes(declarations ...DerivedDeclaration) error {
	derivedRegistry.Lock()
	defer derivedRegistry.Unlock()
	pendingTypes := map[reflect.Type]DerivedDeclaration{}
	pendingIDs := map[string]DerivedDeclaration{}
	pendingDefaults := map[reflect.Type]DerivedDeclaration{}
	for _, declaration := range declarations {
		id, err := concepts.ParseUUID(declaration.ID)
		validID := !declaration.Default && err == nil && id.String() == declaration.ID || declaration.Default && declaration.ID == ""
		if !validID || declaration.Base == nil || declaration.Type == nil {
			return fmt.Errorf("invalid derived declaration %q", declaration.ID)
		}
		base, t := declaration.Base, declaration.Type
		if base.Kind() != reflect.Interface || base.Name() == "" || base.NumMethod() == 0 || !t.Implements(base) {
			return fmt.Errorf("derived type %v must implement a named nonempty base interface %v", t, base)
		}
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || t.Name() == "" || hasCodec(t) {
			return fmt.Errorf("derived type %v requires a named struct without a custom codec", t)
		}
		if err := ValidateType(t); err != nil {
			return err
		}
		members, err := fields(t)
		if err != nil {
			return err
		}
		for _, member := range members {
			if member.name == "_derivedTypeId" {
				return fmt.Errorf("derived discriminator is reserved on %v", t)
			}
		}
		priors := []DerivedDeclaration{derivedRegistry.byType[t], pendingTypes[t]}
		if declaration.Default {
			priors = append(priors, derivedRegistry.defaults[base], pendingDefaults[base])
		} else {
			priors = append(priors, derivedRegistry.byID[declaration.ID], pendingIDs[declaration.ID])
		}
		for _, prior := range priors {
			if prior.Type != nil && prior != declaration {
				return fmt.Errorf("conflicting derived declaration %q for %v", declaration.ID, t)
			}
		}
		pendingTypes[t] = declaration
		if declaration.Default {
			pendingDefaults[base] = declaration
		} else {
			pendingIDs[declaration.ID] = declaration
		}
	}
	for t, declaration := range pendingTypes {
		derivedRegistry.byType[t] = declaration
		if declaration.Default {
			derivedRegistry.defaults[declaration.Base] = declaration
		} else {
			derivedRegistry.byID[declaration.ID] = declaration
		}
		derivedRegistry.bases[declaration.Base] = true
	}
	return nil
}

func derivedFor(t reflect.Type) (DerivedDeclaration, bool) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	derivedRegistry.RLock()
	defer derivedRegistry.RUnlock()
	declaration, ok := derivedRegistry.byType[t]
	return declaration, ok
}

func derivedBase(t reflect.Type) bool {
	derivedRegistry.RLock()
	defer derivedRegistry.RUnlock()
	return derivedRegistry.bases[t]
}

func derivedDiscriminator(data []byte) (string, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", false, fmt.Errorf("derived payload requires an object")
	}
	var id string
	present := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return "", false, err
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return "", false, err
		}
		if token != "_derivedTypeId" {
			continue
		}
		if present {
			return "", false, &DuplicateMemberError{Member: "_derivedTypeId"}
		}
		if err := json.Unmarshal(raw, &id); err != nil || bytes.Equal(raw, []byte("null")) {
			return "", false, fmt.Errorf("invalid derived discriminator")
		}
		present = true
	}
	if _, err := decoder.Token(); err != nil {
		return "", false, err
	}
	return id, present, nil
}

func unmarshalDerived(data []byte, v reflect.Value, depth int) error {
	id, present, err := derivedDiscriminator(data)
	if err != nil {
		return err
	}
	derivedRegistry.RLock()
	declaration, found := derivedRegistry.byID[id]
	if !present {
		declaration, found = derivedRegistry.defaults[v.Type()]
	}
	derivedRegistry.RUnlock()
	if !found || declaration.Base != v.Type() {
		return fmt.Errorf("unknown derived discriminator %q for %v", id, v.Type())
	}
	fresh := reflect.New(declaration.Type).Elem()
	if err := unmarshal(data, fresh, depth+1); err != nil {
		return err
	}
	v.Set(fresh)
	return nil
}
