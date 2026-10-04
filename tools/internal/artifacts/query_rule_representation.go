// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import "go/types"

// QueryRuleRepresentation records compiler facts needed by portable query rules
// in both graph formats. Wire string identity alone does not establish the Go
// value inspected by NewPortable or the behavior of application-owned codecs.
type QueryRuleRepresentation struct {
	GoKind       string `json:"goKind"`
	PointerDepth int    `json:"pointerDepth,omitempty"`
	CustomCodec  bool   `json:"customCodec,omitempty"`
}

func queryRuleRepresentation(t types.Type) *QueryRuleRepresentation {
	representation := &QueryRuleRepresentation{}
	for {
		t = types.Unalias(t)
		for _, method := range []string{"ConceptValue", "MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText"} {
			if hasMethod(t, method) {
				representation.CustomCodec = true
			}
		}
		pointer, ok := t.Underlying().(*types.Pointer)
		if !ok {
			break
		}
		representation.PointerDepth++
		t = pointer.Elem()
	}
	switch underlying := t.Underlying().(type) {
	case *types.Basic:
		representation.GoKind = underlying.Name()
	case *types.Struct:
		representation.GoKind = "struct"
	default:
		representation.GoKind = "other"
	}
	return representation
}
