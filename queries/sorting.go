// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import "strings"

// SortField is an explicit serialized wire field, not a reflected property path.
type SortField string

// SortDirection is the provider-neutral int32 direction.
type SortDirection int32

const (
	// Unspecified means no active order.
	Unspecified SortDirection = iota
	// Ascending selects increasing order.
	Ascending
	// Descending selects decreasing order.
	Descending
)

// Sorting describes an optional active order.
type Sorting struct {
	Field     SortField
	Direction SortDirection
}

// ParseSortDirection accepts only asc/ascending/desc/descending, case-insensitively.
// Missing QUERY direction defaults in ReadQUERY, not this strict parser.
func ParseSortDirection(s string) (SortDirection, error) {
	switch strings.ToLower(s) {
	case "asc", "ascending":
		return Ascending, nil
	case "desc", "descending":
		return Descending, nil
	}
	return Unspecified, ErrInvalidSorting
}
