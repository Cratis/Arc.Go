// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
)

// queryShape separates declared returns/emissions from finalized wire data.
// An element retains pointer nullability for TS preflight; wrappers are not data.
type queryShape struct {
	element                     types.Type
	collection, nullable, paged bool
}

func classifyQueryShape(model, output types.Type) (queryShape, bool) {
	shape := queryShape{}
	output = types.Unalias(output)
	if types.Identical(model, output) {
		return shape, true
	}
	if ptr, ok := output.(*types.Pointer); ok {
		shape.nullable = true
		return shape, types.Identical(types.Unalias(ptr.Elem()), model)
	}
	switch t := output.(type) {
	case *types.Slice:
		shape.element = t.Elem()
	case *types.Array:
		shape.element = t.Elem()
	case *types.Named:
		if (namedType(t, runtimePath+"/queries", "Page") || namedType(t, runtimePath+"/queries", "ObservedCollection")) && t.TypeArgs().Len() == 1 {
			shape.element = t.TypeArgs().At(0)
			shape.paged = namedType(t, runtimePath+"/queries", "Page")
		}
	}
	if shape.element == nil {
		return shape, false
	}
	shape.collection = true
	elem := types.Unalias(shape.element)
	if ptr, ok := elem.(*types.Pointer); ok {
		elem = types.Unalias(ptr.Elem())
	}
	return shape, types.Identical(elem, model)
}

func queryCandidate(model, output types.Type) bool {
	_, recognized, _ := sourceEmission(output)
	return recognized || modelShape(model, output)
}

// sourceEmission recognizes declarations by package/type identity, never by
// structural searching. Ordinary assignment preserves CurrentSource at runtime.
func sourceEmission(output types.Type) (types.Type, bool, error) {
	declared := types.Unalias(output)
	base := declared
	pointer := false
	if ptr, ok := base.(*types.Pointer); ok {
		pointer, base = true, types.Unalias(ptr.Elem())
	}
	named, ok := base.(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != runtimePath+"/observable" {
		return nil, false, nil
	}
	name := named.Obj().Name()
	if name != "Source" && name != "CurrentSource" && name != "State" && name != "Subject" {
		return nil, false, nil
	}
	if named.TypeArgs().Len() != 1 || pointer != (name == "State" || name == "Subject") {
		return nil, true, fmt.Errorf("unsupported observable declaration: use Source[O], CurrentSource[O], *State[O] or *Subject[O]")
	}
	emission := named.TypeArgs().At(0)
	source := named.Obj().Pkg().Scope().Lookup("Source")
	if source == nil {
		return nil, true, fmt.Errorf("observable.Source is unavailable")
	}
	target, err := types.Instantiate(nil, source.Type(), []types.Type{emission}, true)
	if err != nil || !types.AssignableTo(declared, target) {
		return nil, true, fmt.Errorf("observable declaration is not assignable to Source[O]")
	}
	return emission, true, nil
}
