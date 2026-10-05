// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package invalidconcept

import values "github.com/cratis/arc.go/tools/internal/artifacts/testdata/diagnostics/domain"

// Name deliberately lacks the codecs required by the shared concept classifier.
type Name string

func (Name) ConceptValue() string { panic("marker executed") }

type Arguments struct{ Name string }

//arc:readmodel
type View struct{}

func (View) Query(args Arguments) (View, error) {
	_ = values.Name(args.Name)
	_ = Name(args.Name)
	return View{}, nil
}
