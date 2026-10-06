// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package contracttests_test

import (
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

// TestFixturePackagesStayOutOfThePublicAPI keeps every contract-test fixture out of
// the root module's importable surface. A directory under ContractTests with
// non-test Go source is a package that pkg.go.dev publishes with each root-module
// release, so it must live below ContractTests/internal. Test-only directories
// (only _test.go files) are not importable and may stay where they are.
func TestFixturePackagesStayOutOfThePublicAPI(t *testing.T) {
	var internal, exposed []string
	err := fs.WalkDir(os.DirFS("."), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || (name != "." && strings.HasPrefix(entry.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		dir := path.Dir(name)
		if dir == "internal" || strings.HasPrefix(dir, "internal/") {
			internal = append(internal, dir)
		} else {
			exposed = append(exposed, dir)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(exposed)
	if exposed = slices.Compact(exposed); len(exposed) > 0 {
		t.Errorf("fixture packages outside ContractTests/internal are public API of the root module: %v", exposed)
	}
	slices.Sort(internal)
	internal = slices.Compact(internal)
	for _, want := range []string{"internal/generatedconsumer", "internal/observables/clientfixture", "internal/observables/generatedconsumerfixture", "internal/taskboard"} {
		if !slices.Contains(internal, want) {
			t.Errorf("fixture package %s is missing; found %v", want, internal)
		}
	}
}
