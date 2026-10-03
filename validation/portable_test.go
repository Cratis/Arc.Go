// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package validation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cratis/arc.go/validation"
)

type portableRegister struct {
	Name     string `json:"name" rules:"[{\"name\":\"notEmpty\",\"message\":\"Name required\"},{\"name\":\"maxLength\",\"arguments\":[40],\"message\":\"Name too long\"}]"`
	Quantity int    `json:"quantity" rules:"[{\"name\":\"greaterThanOrEqual\",\"arguments\":[1],\"message\":\"Quantity must be positive\"}]"`
}

func TestPortableRuleFoundation(t *testing.T) {
	validator, err := validation.NewPortable[portableRegister]()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		value portableRegister
		count int
	}{
		{portableRegister{}, 2}, {portableRegister{Name: "valid", Quantity: 1}, 0},
		{portableRegister{Name: "\ufeff", Quantity: 1}, 1}, {portableRegister{Name: "\u0085", Quantity: 1}, 0},
		{portableRegister{Name: "😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀", Quantity: 1}, 0},
		{portableRegister{Name: "😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀", Quantity: 1}, 1},
	} {
		results, err := validator.Validate(t.Context(), test.value)
		if err != nil || len(results) != test.count {
			t.Fatalf("%+v: %v, %v", test.value, results, err)
		}
		for _, result := range results {
			if result.Severity != validation.Error || result.Reason != validation.Rule || len(result.Members) != 1 {
				t.Fatal(result)
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := validator.Validate(ctx, portableRegister{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPortableRuleDescriptorRejectsUnprovedContracts(t *testing.T) {
	for _, text := range []string{
		`[{"name":"unknown","message":"bad"}]`,
		`[{"name":"maxLength","arguments":[-1],"message":"bad"}]`,
		`[{"name":"matches","arguments":["(?=x)"],"message":"bad"}]`,
		`[{"name":"matches","arguments":["[[:alpha:]]"],"message":"bad"}]`,
		`[{"name":"maxLength","arguments":[9007199254740992],"message":"bad"}]`,
		`[{"name":"notEmpty","message":"bad","serverOnly":true}]`,
		`[{"name":"notEmpty","message":"bad","severity":99}]`,
		`[{"name":"notEmpty","message":"bad","misspelled":true}]`,
	} {
		if _, err := validation.ParseRules(text, "name", "string"); err == nil {
			t.Fatal("accepted", text)
		}
	}
}
