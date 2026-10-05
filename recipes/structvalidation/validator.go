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

	"github.com/cratis/arc.go/validation"
)

// recipe:start validator-rules

// NewRules returns a validator that reads the `rules` struct tag and reports
// JSON member names. Arc already owns the `validate` tag, which accepts only
// required and skipConcept and fails registration on anything else.
func NewRules() *validator.Validate {
	rules := validator.New(validator.WithRequiredStructEnabled())
	rules.SetTagName("rules")
	rules.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch name {
		case "-":
			return ""
		case "":
			return field.Name
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
			// Namespace is "Type.member.nested[0].field"; drop the Go type.
			_, member, _ := strings.Cut(failure.Namespace(), ".")
			rule := failure.Tag()
			if failure.Param() != "" {
				rule += "=" + failure.Param()
			}
			results = append(results, validation.Result{
				Severity:     validation.Error,
				Message:      fmt.Sprintf("%s does not satisfy %s.", member, rule),
				Members:      []string{member},
				ReasonDetail: &rule,
			})
		}
		return results, nil
	})
}

// recipe:end
