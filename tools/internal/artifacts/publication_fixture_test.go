// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"path/filepath"
	"strings"
	"testing"
)

// copyFixtureFiles retains parent-owned immutable bytes, not a shared workspace
// or graph. Each child receives its own files, including ownership metadata.
func copyFixtureFiles(t *testing.T, source string) func(*testing.T, string) {
	t.Helper()
	inventory := outputInventory(t, source)
	return func(t *testing.T, destination string) {
		t.Helper()
		for relative, entry := range inventory {
			if strings.HasPrefix(entry, "file:") {
				put(t, filepath.Join(destination, relative), strings.TrimPrefix(entry, "file:"))
			}
		}
		assertOutputInventory(t, destination, inventory)
	}
}

// publishedFixture exercises the identical successful bootstrap once per
// parent test. Failure injection, preflight, journal and recovery still run in
// every child, with fresh mutable graphs and output buffers from publication.
func publishedFixture(t *testing.T) func(*testing.T) (string, string, ApplicationProfile, *Graph, []ownedOutput) {
	t.Helper()
	module, root, profile, graph, outputs := publication(t)
	if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err != nil {
		t.Fatal(err)
	}
	copyFiles := copyFixtureFiles(t, module)
	return func(t *testing.T) (string, string, ApplicationProfile, *Graph, []ownedOutput) {
		t.Helper()
		module, root, profile, graph, outputs := publication(t)
		copyFiles(t, module)
		return module, root, profile, graph, outputs
	}
}
