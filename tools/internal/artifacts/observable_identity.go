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
