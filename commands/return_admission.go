// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import (
	"context"
	"reflect"
)

// ReturnClassification is opt-in admission before nil flattening and effects.
type ReturnClassification uint8

const (
	// OrdinaryReturn leaves the existing response/consumer rules unchanged.
	OrdinaryReturn ReturnClassification = iota
	// ServerConsumedReturn requires a consumer and never selects a client response.
	ServerConsumedReturn
)

// ReturnAdmission supplies immutable catalog metadata and optional runtime
// classification. Types contains exact concrete server-consumed types (pointer
// and value are distinct); it is copied on registration. A matching typed nil is
// rejected. With no Types, Check is required and classifies dynamic values.
//
// Check, when supplied, runs before any consumer/updater and may reject a value.
// For a listed type its OrdinaryReturn cannot undo the static claim. It must not
// perform external effects. It runs outside locks and must be concurrent-safe.
// Explicit Respond values and builtin controls bypass this extension entirely.
// No callback runs during Build. This never classifies a slice by its elements.
type ReturnAdmission struct {
	// Types is the frozen set of exact server-consumed types; empty means dynamic.
	Types []reflect.Type
	// Check validates/classifies a borrowed return value, including typed nils.
	Check func(context.Context, *Invocation, any) (ReturnClassification, error)
}

// AddReturnAdmission registers a named admission policy. Static claims compile
// raw return metadata to ResponseNone unless the command explicitly overrides it.
// Dynamic admission retains unknown response metadata and requires runtime checks.
func (r *Registry) AddReturnAdmission(name string, admission ReturnAdmission) error {
	if r == nil || !validExtensionName(name) || (len(admission.Types) == 0 && admission.Check == nil) {
		return ErrInvalidRegistration
	}
	if r.frozen {
		return ErrFrozen
	}
	seen := make(map[reflect.Type]bool)
	for _, typ := range admission.Types {
		if typ == nil || typ.Kind() == reflect.Interface || seen[typ] {
			return ErrInvalidRegistration
		}
		seen[typ] = true
	}
	if r.names == nil {
		r.names = make(map[string]bool)
	}
	if r.names["admission:"+name] {
		return ErrDuplicate
	}
	r.names["admission:"+name] = true
	admission.Types = append([]reflect.Type(nil), admission.Types...)
	r.admissions = append(r.admissions, admission)
	return nil
}

func (a ReturnAdmission) claims(typ reflect.Type) bool {
	for _, listed := range a.Types {
		if listed == typ {
			return true
		}
	}
	return false
}

func (f *frame) admitReturn(value any, kind leafKind) (leafKind, error) {
	if kind == responseLeaf || kind == controlLeaf || value == nil {
		return kind, nil
	}
	if _, builtin := builtinControl(outcomeLeaf{kind: kind, value: value}); builtin {
		return kind, nil
	}
	for _, admission := range f.pipeline.admissions {
		claimed := admission.claims(reflect.TypeOf(value))
		if len(admission.Types) != 0 && !claimed {
			continue
		}
		classification := OrdinaryReturn
		if admission.Check != nil {
			err := f.call(func(ctx context.Context, inv *Invocation) error {
				var err error
				classification, err = admission.Check(ctx, inv, value)
				return err
			})
			if err != nil {
				return kind, err
			}
			if classification > ServerConsumedReturn {
				return kind, ErrInvalidRegistration
			}
		}
		if claimed || classification == ServerConsumedReturn {
			if nilValue(value) {
				return kind, ErrNilReturn
			}
			kind = effectLeaf
		}
	}
	return kind, nil
}
