// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import "reflect"

type leafKind uint8

const (
	valueLeaf leafKind = iota
	effectLeaf
	responseLeaf
	controlLeaf
)

type outcomeLeaf struct {
	kind  leafKind
	value any
}

// Outcome is an explicitly classified return graph. Membership is copied; values
// remain borrowed. Zero means successful void output. Collections are single leaves.
type Outcome[R any] struct{ leaves []outcomeLeaf }

func (Outcome[R]) responseContract() reflect.Type { return reflect.TypeFor[R]() }
func (o Outcome[R]) outcomeLeaves() []outcomeLeaf { return append([]outcomeLeaf(nil), o.leaves...) }

// Respond reserves one response and requires consumption of every effect leaf.
func Respond[R any](response R, effects ...any) Outcome[R] {
	leaves := []outcomeLeaf{{responseLeaf, response}}
	for _, effect := range effects {
		leaves = append(leaves, outcomeLeaf{effectLeaf, effect})
	}
	return Outcome[R]{leaves}
}

// Effects requires every nonnil leaf to be consumed by controls or response handlers.
func Effects[R any](effects ...any) Outcome[R] {
	var leaves []outcomeLeaf
	for _, v := range effects {
		leaves = append(leaves, outcomeLeaf{effectLeaf, v})
	}
	return Outcome[R]{leaves}
}

// Values permits at most one unconsumed response after context-updater processing.
func Values[R any](values ...any) Outcome[R] {
	var leaves []outcomeLeaf
	for _, v := range values {
		leaves = append(leaves, outcomeLeaf{valueLeaf, v})
	}
	return Outcome[R]{leaves}
}

// Control explicitly adopts an infrastructure result rather than serializing it.
func Control[R any](control Result[NoResponse]) Outcome[R] {
	return Outcome[R]{[]outcomeLeaf{{controlLeaf, control}}}
}
func flatten(value any, kind leafKind, depth int, leaves *[]outcomeLeaf, admit func(any, leafKind) (leafKind, error)) error {
	if value == nil {
		return nil
	}
	if depth >= 64 {
		return ErrUnhandledEffect
	}
	if graph, ok := value.(interface{ outcomeLeaves() []outcomeLeaf }); ok && !nilValue(value) {
		for _, leaf := range graph.outcomeLeaves() {
			if err := flatten(leaf.value, leaf.kind, depth+1, leaves, admit); err != nil {
				return err
			}
		}
		return nil
	}
	if _, void := value.(NoResponse); void {
		return nil
	}
	var err error
	kind, err = admit(value, kind)
	if err != nil {
		return err
	}
	if nilValue(value) {
		return nil
	}
	*leaves = append(*leaves, outcomeLeaf{kind, value})
	return nil
}
