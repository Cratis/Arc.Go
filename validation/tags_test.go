// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cratis/arc.go/validation"
)

func TestRequiredTagsAreTopLevelAndZerosAreSupplied(t *testing.T) {
	type child struct {
		Name string `validate:"required"`
	}
	type model struct {
		Name    string   `json:"name" validate:"required"`
		Pointer *int     `json:"pointer" validate:"required"`
		Number  int      `json:"number" validate:"required"`
		Enabled bool     `json:"enabled" validate:"required"`
		Items   []string `json:"items" validate:"required"`
		Child   child    `json:"child"`
	}
	results, err := validation.ValidateTags(model{Name: " \t", Items: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var members []string
	for _, result := range results {
		members = append(members, result.Members...)
	}
	if !reflect.DeepEqual(members, []string{"name", "pointer"}) {
		t.Fatal(members)
	}
	var graph validation.Graph
	results, err = graph.Validate(t.Context(), nil, model{Name: "ok", Pointer: new(int), Items: []string{}})
	if err != nil || len(results) != 0 {
		t.Fatalf("recursive required annotations: %v %v", results, err)
	}
}

func TestInvalidValidationTagsFailExplicitly(t *testing.T) {
	for _, tag := range []string{"required,required", "required=true", "unknown", "skipconcept", "required,", " required"} {
		if _, err := validation.ParseTags(tag); !errors.Is(err, validation.ErrInvalidRegistration) {
			t.Fatalf("%q = %v", tag, err)
		}
	}
	got, err := validation.ParseTags("required,skipConcept")
	if err != nil || !got.Required || !got.SkipConcept {
		t.Fatal(got, err)
	}
}

func FuzzValidationTags(f *testing.F) {
	for _, seed := range []string{"", "required", "skipConcept", "required,skipConcept", "required=x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		tags, err := validation.ParseTags(text)
		if err == nil && text != "" && !tags.Required && !tags.SkipConcept {
			t.Fatal("accepted unsupported tags")
		}
	})
}
