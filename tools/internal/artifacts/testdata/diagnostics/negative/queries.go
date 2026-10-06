// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package negative

import values "github.com/cratis/arc.go/tools/internal/artifacts/testdata/diagnostics/domain"

type Name string // A name/underlying-type lookalike is not a concept.
type NamedPrimitive string

type Embedded struct{ Promoted string }
type Arguments struct {
	Embedded
	Name    string
	Concept values.Name
	Named   NamedPrimitive
	Pointer *string
	Hidden  string `json:"-"`
	private string
	Nested  struct{ Name string }
}

//arc:readmodel
type View struct{}

func (View) AlreadyConcept(args Arguments) (View, error) {
	_ = values.Name(args.Concept)
	return View{}, nil
}

func (View) NonConcept(args Arguments) (View, error) {
	_ = Name(args.Name)
	_ = string(args.Name)
	return View{}, nil
}

func makeName(value string) values.Name { return values.Name(value) }

func (View) FunctionCall(args Arguments) (View, error) {
	_ = makeName(args.Name)
	// Even a concept-named local function is a call, not a type conversion.
	Name := makeName
	_ = Name(args.Name)
	return View{}, nil
}

// These cases define the deliberately narrow syntax boundary, not a safety claim.
func (View) OutsideSyntax(args Arguments, service Arguments) (View, error) {
	_ = values.Name(service.Name)
	_ = values.Name(args.Named)
	_ = values.Name(*args.Pointer)
	_ = values.Name(args.Nested.Name)
	_ = values.Name(args.Promoted)
	_ = values.Name(args.Hidden)
	_ = values.Name(args.private)
	copyOfInput := args.Name
	_ = values.Name(copyOfInput)
	_ = func() values.Name { return values.Name(args.Name) }
	{
		args := Arguments{}
		_ = values.Name(args.Name)
	}
	return View{}, nil
}

// Not an Arc model despite its name and query-shaped methods.
type ReadModel struct{}

func (ReadModel) Query(args Arguments) (ReadModel, error) {
	_ = values.Name(args.Name)
	return ReadModel{}, nil
}

//arc:ignore
type Ignored struct{}

func (Ignored) Query(args Arguments) (Ignored, error) {
	_ = values.Name(args.Name)
	return Ignored{}, nil
}
