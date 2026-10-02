// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package serialization_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cratis/arc.go/commands"
	"github.com/cratis/arc.go/concepts"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/serialization"
	"github.com/cratis/arc.go/validation"
)

func TestFrameworkCyclesAndNestingUseSharedBudget(t *testing.T) {
	finding := validation.Result{}
	finding.State = &finding
	changes := queries.ChangeSet{}
	changes.Added = []any{&changes}
	command := commands.WithResponse(concepts.UUID{}, new(any))
	response, _ := command.Response()
	*response = &command
	query := queries.Success(concepts.UUID{}, new(any))
	data, _ := query.Data()
	*data = &query
	validationCommand := commands.NewResult[any](commands.Details{
		Authorized: true, ValidationResults: []validation.Result{finding},
	}, serialization.Optional[any]{})
	validationQuery := queries.NewResult(queries.Details{
		Authorized: true, Ready: true, ValidationResults: []validation.Result{finding},
	}, serialization.Some[any](nil))
	changeQuery := queries.NewResult(queries.Details{
		Authorized: true, Ready: true, ChangeSet: &changes,
	}, serialization.Some[any](nil))

	for _, tc := range []struct {
		name  string
		value any
	}{
		{"validation state", finding}, {"change items", changes},
		{"command response", command}, {"query data", query},
		{"command findings", validationCommand}, {"query findings", validationQuery},
		{"query changes", changeQuery},
	} {
		for _, encoder := range []struct {
			name   string
			encode func(any) ([]byte, error)
		}{
			{"Arc", serialization.Marshal}, {"JSON", json.Marshal},
		} {
			t.Run(tc.name+"/"+encoder.name, func(t *testing.T) {
				_, err := encoder.encode(tc.value)
				if err == nil || !strings.Contains(err.Error(), "JSON nesting exceeds 64 levels") {
					t.Fatalf("cycle error = %v", err)
				}
			})
		}
	}
	for _, tc := range []struct {
		name string
		wrap func(any) any
	}{
		{"validation", func(value any) any { return validation.Result{State: value} }},
		{"command", func(value any) any { return commands.WithResponse(concepts.UUID{}, value) }},
		{"query", func(value any) any { return queries.Success(concepts.UUID{}, value) }},
		{"changes", func(value any) any { return queries.ChangeSet{Added: []any{value}} }},
		{"mixed", func(value any) any {
			return queries.Success(concepts.UUID{}, commands.WithResponse(concepts.UUID{}, validation.Result{State: queries.ChangeSet{Added: []any{value}}}))
		}},
	} {
		t.Run(tc.name+" nesting", func(t *testing.T) {
			var shallow any = "end"
			for range 3 {
				shallow = tc.wrap(shallow)
			}
			if _, err := serialization.Marshal(shallow); err != nil {
				t.Fatalf("shallow nesting: %v", err)
			}
			var deep any = "end"
			for range 70 {
				deep = tc.wrap(deep)
			}
			for _, encode := range []func(any) ([]byte, error){serialization.Marshal, json.Marshal} {
				if _, err := encode(deep); err == nil || !strings.Contains(err.Error(), "JSON nesting exceeds 64 levels") {
					t.Fatalf("deep nesting error = %v", err)
				}
			}
		})
	}
}
