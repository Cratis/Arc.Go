// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package modelshape_test

import (
	"reflect"
	"testing"

	"github.com/cratis/arc.go/internal/modelshape"
	"github.com/cratis/arc.go/serialization"
)

type Embedded struct {
	Name string
	ID   string
}
type Model struct {
	*Embedded
	Name     string `json:"name"`
	Hidden   string `json:"-"`
	URLValue string
}

func TestFieldsMatchSerializerNamesAndDominance(t *testing.T) {
	fields, err := modelshape.Fields(reflect.TypeFor[Model]())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, field := range fields {
		names = append(names, field.Name)
	}
	if !reflect.DeepEqual(names, []string{"name", "ID", "URLValue"}) {
		t.Fatal(names)
	}
	encoded, err := serialization.Marshal(Model{Embedded: &Embedded{Name: "shadowed", ID: "id"}, Name: "direct", URLValue: "url"})
	if err != nil || string(encoded) != `{"ID":"id","URLValue":"url","name":"direct"}` {
		t.Fatalf("serialized %s: %v", encoded, err)
	}
	if modelshape.Value(reflect.ValueOf(Model{}), fields[1].Index).IsValid() {
		t.Fatal("allocated nil embedding")
	}
	fields[0].Index[0] = 999
	fresh, err := modelshape.Fields(reflect.TypeFor[Model]())
	if err != nil {
		t.Fatal(err)
	}
	if fresh[0].Index[0] == 999 {
		t.Fatal("shared mutable plan")
	}
}

func FuzzOptions(f *testing.F) {
	for _, seed := range []string{"", "required", `default=a\,b\=c\\d`, "required,required", "x=", `x=\q`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		options, err := modelshape.Options(input)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, option := range options {
			if option.Name == "" || seen[option.Name] {
				t.Fatalf("invalid parsed options: %+v", options)
			}
			seen[option.Name] = true
		}
	})
}
