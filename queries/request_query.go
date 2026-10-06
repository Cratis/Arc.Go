// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// ReadQUERY accepts exactly one object envelope. Known object members and argument
// names reject case-insensitive duplicates; unknown envelope members are permissive.
// Paging integer tokens must fit int32; only positive size enables paging.
func ReadQUERY(body []byte) (Request, error) {
	fail := func(err error) (Request, error) { return Request{}, &ReadError{Malformed: true, Cause: err} }
	envelope, err := readObject(body, "arguments", "paging", "sorting")
	if err != nil {
		return fail(err)
	}
	raw := map[string]any{}
	var p Parameters
	if arguments, ok := envelope["arguments"]; ok && !bytes.Equal(bytes.TrimSpace(arguments), []byte("null")) {
		entries, err := readNamedObject(arguments, nil)
		if err != nil {
			return fail(err)
		}
		for k, v := range entries {
			raw[k] = v
		}
	}
	if paging, ok := envelope["paging"]; ok && !bytes.Equal(bytes.TrimSpace(paging), []byte("null")) {
		fields, err := readObject(paging, "page", "pagesize")
		if err != nil {
			return fail(err)
		}
		var page, size int32
		if node, ok := fields["page"]; ok {
			if err := json.Unmarshal(node, &page); err != nil {
				return fail(err)
			}
			if bytes.Equal(bytes.TrimSpace(node), []byte("null")) {
				return fail(ErrMalformedRequest)
			}
		}
		if node, ok := fields["pagesize"]; ok {
			if err := json.Unmarshal(node, &size); err != nil {
				return fail(err)
			}
			if bytes.Equal(bytes.TrimSpace(node), []byte("null")) {
				return fail(ErrMalformedRequest)
			}
		}
		if size > 0 {
			p.Paging = Paging{Page: PageNumber(page), Size: PageSize(size), IsPaged: true}
		}
	}
	if sorting, ok := envelope["sorting"]; ok && !bytes.Equal(bytes.TrimSpace(sorting), []byte("null")) {
		fields, err := readObject(sorting, "field", "direction")
		if err != nil {
			return fail(err)
		}
		var field string
		if node, ok := fields["field"]; ok {
			if err := json.Unmarshal(node, &field); err != nil {
				return fail(err)
			}
		}
		if field != "" {
			direction := "ascending"
			if node, ok := fields["direction"]; ok && !bytes.Equal(bytes.TrimSpace(node), []byte("null")) {
				if err := json.Unmarshal(node, &direction); err != nil {
					return fail(err)
				}
			}
			d, err := ParseSortDirection(direction)
			if err != nil {
				return Request{}, &ReadError{Cause: &SortingError{Field: "sorting.direction", Cause: err}}
			}
			p.Sorting = Sorting{Field: SortField(field), Direction: d}
		}
	}
	a, err := NewArguments(raw)
	if err != nil {
		return fail(err)
	}
	a.source = queryInput
	return NewRequest(a, p), nil
}
func readObject(body []byte, names ...string) (map[string]json.RawMessage, error) {
	declared := make(map[string]bool, len(names))
	for _, name := range names {
		declared[name] = true
	}
	entries, err := readNamedObject(body, declared)
	if err != nil {
		return nil, err
	}
	folded := map[string]json.RawMessage{}
	for k, v := range entries {
		folded[strings.ToLower(k)] = v
	}
	return folded, nil
}
func readNamedObject(body []byte, declared map[string]bool) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, ErrMalformedRequest
	}
	entries := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, ErrMalformedRequest
		}
		key := strings.ToLower(name)
		if seen[key] && (declared == nil || declared[key]) {
			return nil, ErrMalformedRequest
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		entries[name] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.Join(ErrMalformedRequest, err)
	}
	return entries, nil
}
