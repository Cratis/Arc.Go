// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package commands

import "strings"

// ResolvedKey is the provider-neutral command key entry.
const ResolvedKey = "resolvedKey"

// ContextValues owns copied, case-insensitive membership; contained values remain borrowed.
type ContextValues struct{ entries map[string]any }

// NewContextValues copies membership, rejecting empty or case-folded duplicate keys.
func NewContextValues(entries map[string]any) (ContextValues, error) {
	result := ContextValues{entries: make(map[string]any, len(entries))}
	for name, value := range entries {
		if !validExtensionName(name) {
			return ContextValues{}, ErrInvalidRegistration
		}
		name = strings.ToLower(name)
		if _, exists := result.entries[name]; exists {
			return ContextValues{}, ErrDuplicate
		}
		result.entries[name] = value
	}
	return result, nil
}

// Get performs case-insensitive lookup, preserving empty and nil values.
func (v ContextValues) Get(name string) (any, bool) {
	value, ok := v.entries[strings.ToLower(name)]
	return value, ok
}

// Entries returns copied membership, with normalized lower-case keys.
func (v ContextValues) Entries() map[string]any {
	entries := make(map[string]any, len(v.entries))
	for key, value := range v.entries {
		entries[key] = value
	}
	return entries
}
