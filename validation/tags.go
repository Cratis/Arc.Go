// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/cratis/arc.go/internal/modelshape"
)

// Tags is the portable validation annotation subset, separate from graph rules.
type Tags struct {
	// Required rejects nil or empty/whitespace strings in the top-level model.
	// Numeric zero, false and nonnil empty collections are supplied values.
	Required bool
	// SkipConcept suppresses only the immediate child's registered concept rule.
	SkipConcept bool
}

// ParseTags accepts required and skipConcept separated by commas. Values,
// duplicate options and unknown rules are errors, never ignored annotations.
func ParseTags(text string) (Tags, error) {
	options, err := modelshape.Options(text)
	if err != nil {
		return Tags{}, fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
	}
	var tags Tags
	for _, option := range options {
		if option.HasValue {
			return Tags{}, fmt.Errorf("%w: validation tags have no values", ErrInvalidRegistration)
		}
		switch option.Name {
		case "required":
			tags.Required = true
		case "skipConcept":
			tags.SkipConcept = true
		default:
			return Tags{}, fmt.Errorf("%w: unknown validation tag %q", ErrInvalidRegistration, option.Name)
		}
	}
	return tags, nil
}

// ValidateTags runs only the root's portable required annotations. It neither
// invokes model validators nor descends nested models. Nil roots are skipped.
func ValidateTags(model any) ([]Result, error) {
	v := reflect.ValueOf(model)
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return nil, nil
	}
	fields, err := modelshape.Fields(v.Type())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
	}
	var results []Result
	for _, field := range fields {
		tags, err := ParseTags(field.Tag.Get("validate"))
		if err != nil {
			return nil, err
		}
		if tags.Required && missing(modelshape.Value(v, field.Index)) {
			results = append(results, Result{Severity: Error, Message: "The " + field.Name + " field is required.", Members: []string{field.Name}, Reason: Rule})
		}
	}
	return results, nil
}

func missing(v reflect.Value) bool {
	for v.IsValid() {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return true
			}
			v = v.Elem()
		case reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
			return v.IsNil()
		case reflect.String:
			return strings.TrimSpace(v.String()) == ""
		default:
			return false
		}
	}
	return true
}
