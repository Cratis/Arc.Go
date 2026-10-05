// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
)

// validateObservableGoIdentity admits an observable collection element whose
// client key agrees with the server delta key. Like C# ChangeSetComputor and the
// Arc client (7c1e780), an element without any conventional ID/Id member and
// without a serialized id property is identity-less: the server falls back to
// JSON-set deltas and the client removes and reconciles by JSON or position.
func validateObservableGoIdentity(model *types.Named) error {
	members, err := compilerFields(model)
	if err != nil {
		return err
	}
	st := model.Underlying().(*types.Struct)
	// Runtime identity discovery ignores JSON visibility and visits promoted
	// fields too. Reject competing conventional members, including hidden ones,
	// rather than publish a client key that can differ from the server delta key.
	conventional := conventionalIdentityMembers(st, map[*types.Struct]bool{})
	if conventional == 0 {
		for _, member := range members {
			if member.Name == "id" {
				return fmt.Errorf("observable collection serializes id without a conventional identity member ID/Id; the client would key by id while the server delta falls back to JSON comparison")
			}
		}
		return nil
	}
	if conventional != 1 {
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
