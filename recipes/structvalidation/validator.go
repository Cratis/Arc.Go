// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Package structvalidation reports go-playground/validator rules as Arc
// validation results through an ordinary validator callback.
package structvalidation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

// recipe:start validator-rules

// NewRules returns a validator that reads the `playground` struct tag and
// reports Arc wire member names. Arc owns both `validate` (required and
// skipConcept) and `rules` (JSON portable rule descriptors).
func NewRules() *validator.Validate {
	rules := validator.New(validator.WithRequiredStructEnabled())
	rules.SetTagName("playground")
	rules.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch name {
		case "-":
			return ""
		case "":
			return serialization.CamelCase(field.Name)
		}
		return name
	})
	return rules
}

// recipe:end

// recipe:start validator-callback

// Validator adapts rules to an Arc validator for T. Each failed rule becomes
// an Error finding on its wire member, so Arc rejects the command or query
// before its handler runs, on /validate as well as on execution.
func Validator[T any](rules *validator.Validate) validation.Validator[T] {
	return validation.ValidatorFunc[T](func(ctx context.Context, value T) ([]validation.Result, error) {
		err := rules.StructCtx(ctx, value)
		var failures validator.ValidationErrors
		if !errors.As(err, &failures) {
			return nil, err // nil, or a programming error such as a non-struct T.
		}
		results := make([]validation.Result, 0, len(failures))
		for _, failure := range failures {
			member := wireMember(reflect.TypeOf(value), failure.StructNamespace())
			var members []string
			if member != "" {
				members = []string{member}
			}
			rule := failure.Tag()
			if failure.Param() != "" {
				rule += "=" + failure.Param()
			}
			results = append(results, validation.Result{
				Severity:     validation.Error,
				Message:      fmt.Sprintf("%s does not satisfy %s.", member, rule),
				Members:      members,
				ReasonDetail: &rule,
			})
		}
		return results, nil
	})
}

// wireMember maps the Go field namespace, retaining collection indexes but
// omitting anonymous struct segments that Arc flattens on the wire. Unknown
// or hidden fields (including memberless struct-level errors) target the model.
func wireMember(t reflect.Type, namespace string) string {
	_, path, _ := strings.Cut(namespace, ".")
	var members []string
	for path != "" {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return ""
		}
		end := strings.IndexAny(path, ".[")
		if end < 0 {
			end = len(path)
		}
		field, ok := t.FieldByName(path[:end])
		if !ok {
			return ""
		}
		path, t = path[end:], field.Type
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		base := t
		for base.Kind() == reflect.Pointer {
			base = base.Elem()
		}
		if name == "-" || !field.IsExported() && !field.Anonymous {
			return ""
		}
		flatten := field.Anonymous && name == "" && base.Kind() == reflect.Struct
		if name == "" {
			name = serialization.CamelCase(field.Name)
		}
		for strings.HasPrefix(path, "[") {
			end = strings.IndexByte(path, ']')
			if end < 0 {
				return ""
			}
			name += path[:end+1]
			path = path[end+1:]
			for t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			switch t.Kind() {
			case reflect.Array, reflect.Slice, reflect.Map:
				t = t.Elem()
			default:
				return ""
			}
		}
		if !flatten {
			members = append(members, name)
		}
		path = strings.TrimPrefix(path, ".")
	}
	return strings.Join(members, ".")
}

// recipe:end
