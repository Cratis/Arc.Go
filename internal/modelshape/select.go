// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package modelshape

import (
	"fmt"
	"reflect"
	"strings"
)

// WireField is a compiler-independent field-selection result. T is the adapter's
// exact type identity; declaration order, wire names and promotion are shared.
type WireField[T comparable] struct {
	Index                       []int
	Name                        string
	Type                        T
	Tag                         reflect.StructTag
	Tagged, OmitEmpty, OmitZero bool
}

// Member supplies metadata only, never a runtime value or a method invocation.
type Member[T comparable] struct {
	Name                string
	Type                T
	Tag                 reflect.StructTag
	Exported, Anonymous bool
}

// Shape adapts reflection or go/types to the same visibility/dominance kernel.
// Members returns false for nonstructs. Dereference removes at most one pointer.
type Shape[T comparable] struct {
	Members     func(T) ([]Member[T], bool)
	Dereference func(T) T
}

// Select applies Arc wire-field rules without implementing a second selection
// algorithm in the compiler. Returned indices and member slices are owned.
func Select[T comparable](t T, shape Shape[T]) ([]WireField[T], error) {
	if _, ok := shape.Members(t); !ok {
		return nil, fmt.Errorf("expected struct type")
	}
	var candidates []WireField[T]
	ancestors := map[T]bool{}
	var collect func(T, []int) error
	collect = func(t T, prefix []int) error {
		if ancestors[t] {
			return nil
		}
		if len(prefix) >= MaxDepth {
			return fmt.Errorf("model field depth exceeds %d", MaxDepth)
		}
		ancestors[t] = true
		defer delete(ancestors, t)
		members, _ := shape.Members(t)
		for i, member := range members {
			base := shape.Dereference(member.Type)
			_, structBase := shape.Members(base)
			if !member.Exported && (!member.Anonymous || !structBase) {
				continue
			}
			tag := strings.Split(member.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			index := append(append([]int(nil), prefix...), i)
			if member.Anonymous && tag[0] == "" && structBase {
				if err := collect(base, index); err != nil {
					return err
				}
				continue
			}
			name := tag[0]
			if name == "" {
				name = CamelCase(member.Name)
			}
			field := WireField[T]{Index: index, Name: name, Type: member.Type, Tag: member.Tag, Tagged: tag[0] != ""}
			for _, option := range tag[1:] {
				switch option {
				case "omitempty":
					field.OmitEmpty = true
				case "omitzero":
					field.OmitZero = true
				case "":
				default:
					return fmt.Errorf("unsupported JSON option %q", option)
				}
			}
			candidates = append(candidates, field)
		}
		return nil
	}
	if err := collect(t, nil); err != nil {
		return nil, err
	}
	var result []WireField[T]
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate.Name] {
			continue
		}
		seen[candidate.Name] = true
		best, count := candidate, 0
		for _, other := range candidates {
			if other.Name != best.Name {
				continue
			}
			if len(other.Index) < len(best.Index) || len(other.Index) == len(best.Index) && other.Tagged && !best.Tagged {
				best, count = other, 1
			} else if len(other.Index) == len(best.Index) && other.Tagged == best.Tagged {
				count++
			}
		}
		if count > 1 {
			if len(best.Index) == 1 {
				return nil, fmt.Errorf("ambiguous JSON members %q and %q", best.Name, best.Name)
			}
			continue
		}
		result = append(result, best)
	}
	return result, nil
}
