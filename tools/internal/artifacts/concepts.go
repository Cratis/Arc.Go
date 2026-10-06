// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"

	"github.com/cratis/fundamentals.go/concepts"
	"github.com/cratis/fundamentals.go/concepts/conceptstypes"
)

// conceptWire is only a projection of Fundamentals' shared classifier. It never
// reconstructs marker signatures, validates codecs independently, or invokes code.
func conceptWire(t types.Type) (WireType, bool, error) {
	representation, recognized, err := conceptstypes.Underlying(t)
	if err != nil || !recognized {
		return WireType{}, recognized, err
	}
	kind := ""
	switch representation.Kind {
	case concepts.KindString:
		kind = "string"
	case concepts.KindBool:
		kind = "boolean"
	case concepts.KindInt, concepts.KindInt8, concepts.KindInt16, concepts.KindInt32, concepts.KindInt64,
		concepts.KindUint, concepts.KindUint8, concepts.KindUint16, concepts.KindUint32, concepts.KindUint64,
		concepts.KindFloat32, concepts.KindFloat64:
		kind = "number"
	case concepts.KindUUID:
		kind = "Guid"
	case concepts.KindDateOnly:
		kind = "DateOnly"
	case concepts.KindTimeOnly:
		kind = "TimeOnly"
	case concepts.KindTimeSpan:
		kind = "TimeSpan"
	default:
		return WireType{}, false, fmt.Errorf("unsupported shared concept scalar kind %s", representation.Kind)
	}
	return WireType{Kind: kind, Nullable: representation.PointerDepth > 0}, true, nil
}
