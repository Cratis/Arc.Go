// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"strconv"
)

// validateTypeScriptContract admits a selected closure of the shared normalized
// graph. It neither erases API nodes nor repeats type/field/route discovery.
func (w *wireAnalyzer) validateTypeScriptContract() error {
	reached := map[string]bool{}
	var visit func(WireType) error
	visit = func(wire WireType) error {
		switch wire.Kind {
		case "array":
			if wire.Contract != nil && wire.Contract.ByteArray {
				return fmt.Errorf("binary arrays require an explicit wire contract (not inferred UUID)")
			}
		case "optional":
			return fmt.Errorf("wire type %s: serialization.Optional requires a presence-compatible surface; strict C# proxy mode does not erase explicit null", wire.Contract.Declared)
		case "declared":
			return fmt.Errorf("wire type %s: schema assertions do not establish TypeScript hydration capability", wire.Target)
		case "record":
			if wire.Element == nil || wire.Element.Kind != "string" && wire.Element.Kind != "number" && wire.Element.Kind != "boolean" && wire.Element.Kind != "enum" {
				return fmt.Errorf("rich dictionary values cannot hydrate through Object metadata; an explicit codec/mapping is required")
			}
		}
		if wire.Element != nil {
			if err := visit(*wire.Element); err != nil {
				return err
			}
		}
		if wire.Target == "" || reached[wire.Target] {
			return nil
		}
		node := w.nodes[wire.Target]
		if node == nil {
			return fmt.Errorf("unknown TypeScript wire target %s", wire.Target)
		}
		reached[wire.Target] = true
		if node.Kind == "enum" {
			for _, member := range node.Members {
				value, err := strconv.ParseInt(member.Value, 10, 64)
				if err != nil || value < -9007199254740991 || value > 9007199254740991 || node.Flags && (value < -2147483648 || value > 2147483647) {
					return fmt.Errorf("enum value %s exceeds safe number/bitwise limits", member.Name)
				}
			}
		}
		if node.Base != "" {
			if err := visit(WireType{Kind: "model", Target: node.Base}); err != nil {
				return err
			}
		}
		for _, field := range node.Fields {
			if field.Name == "constructor" || field.Name == "__proto__" || field.Name == "prototype" {
				return fmt.Errorf("reserved frontend wire field %q", field.Name)
			}
			if err := visit(field.Type); err != nil {
				return err
			}
		}
		return nil
	}
	for _, wire := range w.tsRoots {
		if err := visit(wire); err != nil {
			return err
		}
	}
	var nodes []TypeDescriptor
	for i := range w.graph.Types {
		included := reached[w.graph.Types[i].Key]
		w.graph.Types[i].TSIncluded = &included
		if included {
			nodes = append(nodes, w.graph.Types[i])
		}
	}
	return rejectConstructorCycles(nodes)
}
