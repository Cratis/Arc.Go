// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTraversalCodecProductionCLIRefusesBeforeAnyPublication(t *testing.T) {
	binary := buildArcGenCLI(t)
	for _, version := range []int{GraphVersion, ContractGraphVersion} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			dir := consumer(t)
			profile := ApplicationProfile{FormatVersion: version, Name: "codec-publication", TypeScript: TypeScriptProfile{Out: "web"}}
			encoded, err := json.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			profilePath := filepath.Join(dir, "profile.json")
			put(t, profilePath, string(encoded))
			inputPath := filepath.Join(dir, "input.go")
			put(t, inputPath, codecCommandSource("type Opaque struct { Exported string }", "Opaque"))
			cli := func(check bool) ([]byte, error) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				args := []string{"-dir", dir, "-config", profilePath}
				if check {
					args = append(args, "-check")
				}
				command := exec.CommandContext(ctx, binary, append(args, ".")...)
				command.Dir = "../.."
				command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
				return command.CombinedOutput()
			}
			for _, check := range []bool{false, true} {
				if output, err := cli(check); err != nil {
					t.Fatalf("seed generation/check=%v: %v\n%s", check, err, output)
				}
				if !check {
					tidyConsumer(t, dir)
				}
			}
			seed := outputInventory(t, dir)
			adapter, proxy, manifest := false, false, false
			for path := range seed {
				adapter = adapter || filepath.Base(path) == Filename
				proxy = proxy || strings.HasSuffix(path, ".ts")
				manifest = manifest || filepath.Base(path) == ".arc-gen-manifest.json"
			}
			if !adapter || !proxy || !manifest {
				t.Fatal("seed did not publish all owned output families")
			}
			// Inventory the complete consumer after the intentional source edit,
			// not just selected generated files. Refusal must create/delete nothing.
			put(t, inputPath, codecCommandSource("type Opaque struct { Exported string }"+traversalCodecPanicMethod, "Opaque"))
			before := outputInventory(t, dir)
			for _, check := range []bool{false, true} {
				if output, err := cli(check); err == nil || !bytes.Contains(output, []byte("opaque custom codec")) || !bytes.Contains(output, []byte("MarshalJSONWith")) {
					t.Fatalf("generation/check=%v accepted opaque hook: %v\n%s", check, err, output)
				}
				assertOutputInventory(t, dir, before)
			}
		})
	}
}
