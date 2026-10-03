// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestProductionCLIPlansPublishesAndChecksActualRuntimeContract(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "Publication")
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), string(get(t, filepath.Join(fixture, "input.go.txt"))))
	put(t, filepath.Join(dir, "shared", "shared.go"), string(get(t, filepath.Join(fixture, "shared.go.txt"))))
	put(t, filepath.Join(dir, "profile.json"), string(get(t, filepath.Join(fixture, "profile.json"))))
	cli := func(extra ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		args := []string{"run", "./cmd/arc-gen", "-dir", dir, "-config", filepath.Join(dir, "profile.json")}
		args = append(args, extra...)
		args = append(args, ".")
		command := exec.CommandContext(ctx, "go", args...)
		command.Dir = "../.."
		command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
		return command.CombinedOutput()
	}
	if output, err := cli(); err != nil {
		t.Fatalf("production CLI failed: %v\n%s", err, output)
	}
	if output, err := cli("-check"); err != nil {
		t.Fatalf("production CLI check failed: %v\n%s", err, output)
	}
	var inventory []string
	if err := filepath.WalkDir(filepath.Join(dir, "web"), func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(filepath.Join(dir, "web"), path)
		if err != nil {
			return err
		}
		inventory = append(inventory, filepath.ToSlash(relative))
		expected := filepath.Join(fixture, "Generated", relative)
		if os.Getenv("ARC_UPDATE_PUBLICATION_FIXTURE") == "1" {
			put(t, expected, string(get(t, path)))
		}
		if !bytes.Equal(get(t, expected), get(t, path)) {
			t.Fatalf("CLI output fixture differs: %s", relative)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	expected := []string{manifestName, "Shared/Detail.ts", "Shared/Status.ts", "Shared/index.ts", "Tasks/All.ts", "Tasks/Echo.ts", "Tasks/Find.ts", "Tasks/Listing.ts", "Tasks/index.ts"}
	sort.Strings(expected)
	sort.Strings(inventory)
	if strings.Join(expected, "\n") != strings.Join(inventory, "\n") {
		t.Fatalf("incomplete production inventory: %v", inventory)
	}
	adapter := get(t, filepath.Join(dir, Filename))
	if !bytes.Contains(adapter, []byte("ExpectGeneratedEndpoints")) || !bytes.Contains(adapter, []byte("NewPortable[Echo]")) || !bytes.Contains(adapter, []byte("WithResponseType[Echo, Listing]")) {
		t.Fatal("generated server/proxy agreement or rule registration absent")
	}
	put(t, filepath.Join(dir, "contract_test.go"), string(get(t, filepath.Join(fixture, "contract_test.go.txt"))))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated runtime contract failed at fetchable tools pin: %v\n%s", err, output)
	}
	// An unsupported source anywhere in the selected profile must not publish a
	// changed adapter, proxy, manifest, or journal.
	original := get(t, filepath.Join(dir, "web", manifestName))
	put(t, filepath.Join(dir, "bad.go"), "package consumer\n//arc:command\ntype Bad struct { Dynamic any }\nfunc (Bad) Handle() error { return nil }\n")
	if output, err := cli(); err == nil {
		t.Fatalf("partial-profile false success: %s", output)
	}
	if !bytes.Equal(adapter, get(t, filepath.Join(dir, Filename))) || !bytes.Equal(original, get(t, filepath.Join(dir, "web", manifestName))) {
		t.Fatal("failed production preflight wrote partial output")
	}
}
