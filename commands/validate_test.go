// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/validation"
)

type Validated struct {
	Name     string              `json:"name" validate:"required"`
	Severity validation.Severity `json:"severity"`
}

func (c Validated) Validate(context.Context) ([]validation.Result, error) {
	return []validation.Result{{Severity: c.Severity, Members: []string{"name"}, Message: "rule"}}, nil
}
func (Validated) Handle(context.Context) error { return nil }
func TestModelValidationAndSeverityFloor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		floor     *validation.Severity
		allowance *validation.Severity
		severity  validation.Severity
		success   bool
	}{
		{"ordinary warning omitted", nil, nil, validation.Warning, true},
		{"error", nil, nil, validation.Error, false},
		{"floor warning", severity(validation.Warning), severity(validation.Error), validation.Warning, false},
		{"unknown attributed", severity(validation.Error), nil, validation.Unknown, false},
		{"caller tightens", nil, severity(validation.Unknown), validation.Information, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r commands.Registry
			opts := []commands.Option[Validated]{}
			if tc.floor != nil {
				opts = append(opts, commands.WithBlockOnValidationSeverity[Validated](*tc.floor))
			}
			must(t, commands.Register[Validated](&r, opts...))
			p := build(t, &r, commands.PipelineOptions{})
			result, err := p.Validate(t.Context(), Validated{"supplied", tc.severity}, commands.ExecuteOptions{AllowedSeverity: tc.allowance})
			must(t, err)
			if result.IsSuccess() != tc.success {
				t.Fatal(result.Details())
			}
		})
	}
	var r commands.Registry
	must(t, commands.Register[Validated](&r))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Validate(t.Context(), Validated{" ", validation.Warning})
	must(t, err)
	if result.IsValid() {
		t.Fatal("required tag did not run")
	}
}
func severity(s validation.Severity) *validation.Severity { return &s }

type Email string
type GraphCommand struct {
	Emails []Email `json:"emails"`
}

func (GraphCommand) Handle(context.Context) error { return nil }
func TestConceptGraphAndExplicitValidatorsAreAdditive(t *testing.T) {
	var rules validation.Registry
	must(t, validation.RegisterConcept(&rules, validation.ValidatorFunc[Email](func(context.Context, Email) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Error, Members: []string{"value"}}}, nil
	})))
	graph, err := rules.Build()
	must(t, err)
	var r commands.Registry
	must(t, commands.Register[GraphCommand](&r, commands.WithValidator(validation.ValidatorFunc[GraphCommand](func(context.Context, GraphCommand) ([]validation.Result, error) {
		return []validation.Result{{Severity: validation.Error, Members: []string{"explicit"}}}, nil
	}))))
	p := build(t, &r, commands.PipelineOptions{Validation: graph})
	result, err := p.Validate(t.Context(), GraphCommand{[]Email{"invalid"}})
	must(t, err)
	// Explicit validators remain additive to the concept graph.
	if got := result.Details().ValidationResults; len(got) != 2 || got[0].Members[0] != "emails" || got[1].Members[0] != "explicit" {
		t.Fatal(got)
	}
}
func TestValidateExcludesParticipantsPreparationAndResponseHandlers(t *testing.T) {
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.Scoped(func(context.Context, *resource) (commands.Handler[Clear, string], error) {
		t.Fatal("handler factory during Validate")
		return commands.Handler[Clear, string]{}, nil
	})))
	must(t, r.AddExecutionScope("excluded", func(context.Context, *execution.Scope) (commands.ExecutionScope, error) {
		t.Fatal("participant during Validate")
		return nil, nil
	}))
	must(t, r.AddResponseValueHandler("excluded", func(context.Context, *execution.Scope) (commands.ResponseValueHandler, error) {
		t.Fatal("response factory during Validate")
		return nil, nil
	}))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Validate(t.Context(), Clear{})
	must(t, err)
	if !result.IsSuccess() {
		t.Fatal(result.Details())
	}
}
func TestValidatorFailureClassificationAndOptOut(t *testing.T) {
	cause := errors.New("secret validator detail")
	var r commands.Registry
	must(t, commands.Register[Clear](&r, commands.WithValidator(validation.ValidatorFunc[Clear](func(context.Context, Clear) ([]validation.Result, error) { return nil, cause }))))
	must(t, commands.Register[Validated](&r, commands.WithoutModelValidation[Validated]()))
	p := build(t, &r, commands.PipelineOptions{})
	result, err := p.Validate(t.Context(), Clear{})
	if !errors.Is(err, cause) || result.HasExceptions() || result.IsValid() {
		t.Fatal(result.Details(), err)
	}
	if got := result.Details().ValidationResults; len(got) != 1 || got[0].Reason != validation.ValidatorFailed || got[0].Message != "The value could not be validated." {
		t.Fatal(got)
	}
	result, err = p.Validate(t.Context(), Validated{})
	must(t, err)
	if !result.IsSuccess() {
		t.Fatal(result.Details())
	}
}
