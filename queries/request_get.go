// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"net/url"
	"strconv"
	"strings"
)

// ReadGET preserves raw presence and CSV inputs. Parsed int32 pageSize enables
// paging even when nonpositive; invalid/missing page defaults to zero.
// Case-insensitive duplicate controls (including wait controls) are rejected.
func ReadGET(values url.Values) (Request, error) {
	controls := map[string]string{}
	raw := map[string]any{}
	for name, entries := range values {
		if reserved(name) {
			key := strings.ToLower(name)
			if _, exists := controls[key]; exists || len(entries) != 1 {
				return Request{}, &ReadError{Malformed: true, Cause: ErrInvalidArguments}
			}
			controls[key] = entries[0]
		} else {
			raw[name] = append([]string(nil), entries...)
		}
	}
	a, err := NewArguments(raw)
	if err != nil {
		return Request{}, &ReadError{Malformed: true, Cause: err}
	}
	a.source = getInput
	var p Parameters
	if size, err := strconv.ParseInt(strings.TrimSpace(controls["pagesize"]), 10, 32); err == nil {
		page, _ := strconv.ParseInt(strings.TrimSpace(controls["page"]), 10, 32)
		// Overflowing and malformed page are both zero, not ParseInt's saturated value.
		if _, err := strconv.ParseInt(strings.TrimSpace(controls["page"]), 10, 32); err != nil {
			page = 0
		}
		p.Paging = Paging{Page: PageNumber(page), Size: PageSize(size), IsPaged: true}
	}
	field, direction := controls["sortby"], controls["sortdirection"]
	if field != "" && direction != "" {
		d, err := ParseSortDirection(direction)
		if err != nil {
			return Request{}, &ReadError{Cause: &SortingError{Field: "sortDirection", Cause: err}}
		}
		p.Sorting = Sorting{Field: SortField(field), Direction: d}
	}
	return NewRequest(a, p), nil
}
