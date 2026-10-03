// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
)

func validateObservableGoIdentity(model *types.Named) error {
	members, err := compilerFields(model)
	if err != nil {
		return err
	}
	st := model.Underlying().(*types.Struct)
	// Runtime identity discovery ignores JSON visibility and visits promoted
	// fields too. Reject competing conventional members, including hidden ones,
	// rather than publish a client key that can differ from the server delta key.
	if conventionalIdentityMembers(st, map[*types.Struct]bool{}) != 1 {
		return fmt.Errorf("observable collection requires one unambiguous conventional identity member ID/Id, including JSON-hidden and embedded members")
	}
	for _, member := range members {
		if member.Name != "id" || len(member.Index) != 1 || member.OmitEmpty || member.OmitZero {
			continue
		}
		name := st.Field(member.Index[0]).Name()
		if name == "ID" || name == "Id" {
			return nil
		}
	}
	return fmt.Errorf("observable collection requires a selected direct ID/Id serialized as id; identity metadata alone is not a client delta extractor")
}

func conventionalIdentityMembers(st *types.Struct, seen map[*types.Struct]bool) int {
	if seen[st] {
		return 0
	}
	seen[st] = true
	defer delete(seen, st)
	count := 0
	for i := 0; i < st.NumFields(); i++ {
		field := st.Field(i)
		if field.Exported() && (field.Name() == "ID" || field.Name() == "Id") {
			count++
		}
		if !field.Embedded() {
			continue
		}
		typ := types.Unalias(field.Type())
		if pointer, ok := typ.(*types.Pointer); ok {
			typ = types.Unalias(pointer.Elem())
		}
		if embedded, ok := typ.Underlying().(*types.Struct); ok {
			count += conventionalIdentityMembers(embedded, seen)
		}
	}
	return count
}
