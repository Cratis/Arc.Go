// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"
)

// validateGoQueryDefault mirrors queries/binding.go's primitive text grammar
// before scalar wire reduction. Custom codecs are never executed or assumed to
// accept the primitive grammar. sizes is the loaded target's compiler sizing.
func validateGoQueryDefault(t types.Type, text string, sizes types.Sizes) error {
	t = types.Unalias(t)
	if pointer, ok := t.(*types.Pointer); ok {
		t = types.Unalias(pointer.Elem())
	}
	if named, ok := t.(*types.Named); ok {
		for _, set := range []*types.MethodSet{types.NewMethodSet(named), types.NewMethodSet(types.NewPointer(named))} {
			if set.Lookup(nil, "UnmarshalText") != nil || set.Lookup(nil, "UnmarshalJSON") != nil {
				return fmt.Errorf("custom scalar default requires an explicit proven binder contract")
			}
		}
	}
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return fmt.Errorf("unsupported server default type %s", t)
	}
	var err error
	switch {
	case basic.Kind() == types.String:
		return nil
	case basic.Kind() == types.Bool:
		value := strings.ToLower(strings.TrimSpace(text))
		if value == "true" || value == "false" {
			return nil
		}
		return fmt.Errorf("boolean server default must be true or false")
	case basic.Info()&types.IsInteger != 0:
		bits := int(sizes.Sizeof(basic) * 8)
		if basic.Info()&types.IsUnsigned != 0 {
			_, err = strconv.ParseUint(text, 10, bits)
		} else {
			_, err = strconv.ParseInt(text, 10, bits)
		}
	case basic.Info()&types.IsFloat != 0:
		_, err = strconv.ParseFloat(text, int(sizes.Sizeof(basic)*8))
	default:
		return fmt.Errorf("unsupported server default type %s", t)
	}
	return err
}
