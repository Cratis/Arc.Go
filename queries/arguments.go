// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import (
	"encoding/json"
	"strings"
)

type provenance uint8

const (
	directInput provenance = iota
	getInput
	queryInput
)

// Arguments owns case-insensitive raw input membership and transport provenance.
// Missing, explicit null and empty input remain inspectable even when binding omits them.
// Ordinary application objects remain borrowed; byte/string slices are copied.
type Arguments struct {
	entries map[string]any
	names   map[string]string
	source  provenance
}

// NewArguments copies membership, rejecting empty/case-insensitive duplicate names.
func NewArguments(entries map[string]any) (Arguments, error) {
	a := Arguments{entries: map[string]any{}, names: map[string]string{}}
	for name, value := range entries {
		key := strings.ToLower(name)
		if name == "" || a.names[key] != "" {
			return Arguments{}, ErrInvalidArguments
		}
		a.names[key] = name
		a.entries[name] = copyRaw(value)
	}
	return a, nil
}

// Get looks up a raw supplied value case-insensitively. Nil with true is explicit null.
func (a Arguments) Get(name string) (any, bool) {
	n, ok := a.names[strings.ToLower(name)]
	if !ok {
		return nil, false
	}
	return copyRaw(a.entries[n]), true
}

// Entries returns isolated raw membership, not deep clones of application objects.
func (a Arguments) Entries() map[string]any {
	m := map[string]any{}
	for k, v := range a.entries {
		m[k] = copyRaw(v)
	}
	return m
}
func copyRaw(v any) any {
	switch v := v.(type) {
	case json.RawMessage:
		return append(json.RawMessage(nil), v...)
	case []byte:
		return append([]byte(nil), v...)
	case []string:
		return append([]string(nil), v...)
	default:
		return v
	}
}
func reserved(name string) bool {
	switch strings.ToLower(name) {
	case "page", "pagesize", "sortby", "sortdirection", "waitforfirstresult", "waitforfirstresulttimeout":
		return true
	}
	return false
}
