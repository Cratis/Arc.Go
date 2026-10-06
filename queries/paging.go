// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"encoding/json"
	"math"
)

// PagingInfo is response metadata, not a request or an instruction to page data.
// The zero value matches C# NotPaged. Counts and page sizes retain C# numeric widths.
type PagingInfo struct {
	// Page is the zero-based page number.
	Page int32 `json:"page"`
	// Size is the response page size (request readers use pageSize instead).
	Size int32 `json:"size"`
	// TotalItems is the provider's total, not necessarily the returned slice length.
	TotalItems int64 `json:"totalItems"`
}

// TotalPages follows C#'s double-precision ceiling, including signed values.
// Out-of-range totals return MinInt32 deterministically; parity with a particular
// .NET runtime's unchecked overflow conversion is not established. Request
// validation belongs to the pipeline. A zero size always returns zero.
func (p PagingInfo) TotalPages() int32 {
	if p.Size == 0 {
		return 0
	}
	pages := math.Ceil(float64(p.TotalItems) / float64(p.Size))
	if pages > math.MaxInt32 || pages < math.MinInt32 {
		return math.MinInt32
	}
	return int32(pages)
}

// MarshalJSON always includes totalPages along with page, size and totalItems.
func (p PagingInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Page       int32 `json:"page"`
		Size       int32 `json:"size"`
		TotalItems int64 `json:"totalItems"`
		TotalPages int32 `json:"totalPages"`
	}{p.Page, p.Size, p.TotalItems, p.TotalPages()})
}
