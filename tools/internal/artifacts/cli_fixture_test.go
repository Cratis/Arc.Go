// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// buildArcGenCLI builds the production entry point once for its parent test.
// Each invocation still runs a separate process against its own consumer state.
func buildArcGenCLI(t *testing.T) string {
	t.Helper()
	name := "arc-gen"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/arc-gen")
	command.Dir = "../.."
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build production arc-gen CLI: %v\n%s", err, output)
	}
	return binary
}
