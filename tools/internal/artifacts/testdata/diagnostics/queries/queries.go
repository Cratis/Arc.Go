// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"context"

	q "github.com/cratis/arc.go/queries"
	values "github.com/cratis/arc.go/tools/internal/artifacts/testdata/diagnostics/domain"
)

type PrimitiveAlias = string
type ConceptAlias = values.Name
type ArgumentAlias = Arguments

type Arguments struct {
	Name  string
	Alias PrimitiveAlias
	Count int
	Flag  bool
	Ratio float64
}

//arc:readmodel
type View struct{ Name string }

// A discovered method on an opted-in model is a query without arc:query.
func (View) ByName(_ context.Context, args Arguments, _ q.QueryContext) (View, error) {
	_ = values.Name(args.Name)     // want ARC0015 args.Name
	_ = ConceptAlias((args.Alias)) // want ARC0015 args.Alias
	_ = values.Count(args.Count)   // want ARC0015 args.Count
	_ = values.Enabled(args.Flag)  // want ARC0015 args.Flag
	_ = values.Ratio(args.Ratio)   // want ARC0015 args.Ratio
	panic("query executed")
}

//arc:query model=View
func ByAlias(args ArgumentAlias) ([]View, error) {
	_ = (ConceptAlias)((args).Alias) // want ARC0015 (args).Alias
	panic("query executed")
}

// Query helper lookalikes are not admitted unless explicitly marked.
func ByName(args Arguments) (View, error) {
	_ = values.Name(args.Name)
	panic("non-query executed")
}

//arc:ignore
func (View) Ignored(args Arguments) (View, error) {
	_ = values.Name(args.Name)
	panic("ignored query executed")
}

func (view View) NamedReceiver(args Arguments) (View, error) {
	_ = values.Name(args.Name)
	return view, nil
}

func (*View) PointerReceiver(args Arguments) (View, error) {
	_ = values.Name(args.Name)
	panic("non-query executed")
}

func (View) private(args Arguments) (View, error) {
	_ = values.Name(args.Name)
	panic("non-query executed")
}

func (View) NotAQuery(args Arguments) (string, error) {
	return string(values.Name(args.Name)), nil
}
