// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import "go/types"

// codecMethodNames inventories opaque codec names without executing application
// code or claiming their signatures establish a wire contract. Pointer and
// promoted methods conservatively invalidate inference just like value methods.
func codecMethodNames(t types.Type) []string {
	for {
		t = types.Unalias(t)
		pointer, ok := t.Underlying().(*types.Pointer)
		if !ok {
			break
		}
		t = pointer.Elem()
	}
	sets := []*types.MethodSet{types.NewMethodSet(t), types.NewMethodSet(types.NewPointer(t))}
	var names []string
	for _, name := range []string{"MarshalJSON", "MarshalJSONWith", "MarshalText", "UnmarshalJSON", "UnmarshalText"} {
		for _, set := range sets {
			if set.Lookup(nil, name) != nil {
				names = append(names, name)
				break
			}
		}
	}
	return names
}
