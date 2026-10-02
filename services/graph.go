// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package services

import "slices"

func orderedKeys(bindings map[Key]binding) []Key {
	keys := make([]Key, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b Key) int {
		if a.String() < b.String() {
			return -1
		}
		if a.String() > b.String() {
			return 1
		}
		return 0
	})
	return keys
}
func orderedDependencies(b binding) []Key {
	deps := slices.Clone(b.dependencies)
	slices.SortFunc(deps, func(a, b Key) int {
		if a.String() < b.String() {
			return -1
		}
		if a.String() > b.String() {
			return 1
		}
		return 0
	})
	return deps
}
func validateGraph(bindings map[Key]binding) error {
	keys := orderedKeys(bindings)
	for _, key := range keys {
		for _, dep := range orderedDependencies(bindings[key]) {
			if _, exists := bindings[dep]; !exists {
				return failure("build", dep, []Key{key, dep}, ErrMissing, nil)
			}
		}
	}
	visited := map[Key]bool{}
	var visit func(Key, []Key) error
	visit = func(key Key, path []Key) error {
		if slices.Contains(path, key) {
			return failure("build", key, append(slices.Clone(path), key), ErrCycle, nil)
		}
		if visited[key] {
			return nil
		}
		path = append(slices.Clone(path), key)
		for _, dep := range orderedDependencies(bindings[key]) {
			if err := visit(dep, path); err != nil {
				return err
			}
		}
		visited[key] = true
		return nil
	}
	for _, key := range keys {
		if err := visit(key, nil); err != nil {
			return err
		}
	}
	var captive func(Key, []Key, map[Key]bool) error
	captive = func(key Key, path []Key, seen map[Key]bool) error {
		path = append(slices.Clone(path), key)
		if bindings[key].lifetime == Scoped {
			return failure("build", key, path, ErrCaptiveLifetime, nil)
		}
		if seen[key] {
			return nil
		}
		seen[key] = true
		for _, dep := range orderedDependencies(bindings[key]) {
			if err := captive(dep, path, seen); err != nil {
				return err
			}
		}
		return nil
	}
	for _, key := range keys {
		if bindings[key].lifetime == Singleton {
			if err := captive(key, nil, map[Key]bool{}); err != nil {
				return err
			}
		}
	}
	return nil
}
