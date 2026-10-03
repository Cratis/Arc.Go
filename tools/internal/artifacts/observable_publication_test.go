// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionCLIObservableMixedFamilyPublication(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "ContractTests", "ProxyComparison", "Observables")
	dir := consumer(t)
	input := string(get(t, filepath.Join(fixture, "input.go.txt")))
	put(t, filepath.Join(dir, "input.go"), input)
	put(t, filepath.Join(dir, "profile.json"), string(get(t, filepath.Join(fixture, "profile.json"))))
	cli := func(extra ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		args := append([]string{"run", "./cmd/arc-gen", "-dir", dir, "-config", filepath.Join(dir, "profile.json")}, extra...)
		cmd := exec.CommandContext(ctx, "go", append(args, ".")...)
		cmd.Dir, cmd.Env = "../..", append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
		return cmd.CombinedOutput()
	}
	if output, err := cli(); err != nil {
		t.Fatalf("production observable CLI: %v\n%s", err, output)
	}
	before := outputInventory(t, dir)
	if output, err := cli("-check"); err != nil {
		t.Fatalf("production observable check: %v\n%s", err, output)
	}
	if output, err := cli(); err != nil {
		t.Fatalf("regeneration: %v\n%s", err, output)
	}
	assertOutputInventory(t, dir, before)
	count := 0
	if err := filepath.WalkDir(filepath.Join(dir, "web"), func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.Join(dir, "web"), file)
		if err != nil {
			return err
		}
		expected := filepath.Join(fixture, "Generated", rel)
		if os.Getenv("ARC_UPDATE_OBSERVABLE_FIXTURE") == "1" {
			put(t, expected, string(get(t, file)))
		}
		if !bytes.Equal(get(t, expected), get(t, file)) {
			t.Fatalf("stale production fixture: %s", rel)
		}
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 17 {
		t.Fatalf("incomplete fixture: %d files", count)
	}
	put(t, filepath.Join(dir, "contract_test.go"), string(get(t, "testdata/observable_consumer_test.go")))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("production generated adapter contract: %v\n%s", err, output)
	}
	// Unknown emissions remain candidates and prevent every write, even when a
	// valid family's authored inputs have changed concurrently.
	put(t, filepath.Join(dir, "input.go"), strings.Replace(input, "Title string", "Extra string `json:\"extra\"`\n Title string", 1)+"\nfunc (Task) Unsupported() (observable.Source[string],error) { return nil,nil }\n")
	before = outputInventory(t, dir)
	if output, err := cli(); err == nil {
		t.Fatalf("unsupported emission published: %s", output)
	}
	assertOutputInventory(t, dir, before)
	put(t, filepath.Join(dir, "input.go"), input)
	// Snapshot/observable transitions use the same publisher ownership lane.
	put(t, filepath.Join(dir, "input.go"), strings.Replace(input, "func (Task) Single() (observable.Source[Task], error)", "func (Task) Single() (Task, error)", 1))
	// The body must change with its authored signature as well.
	put(t, filepath.Join(dir, "input.go"), strings.Replace(string(get(t, filepath.Join(dir, "input.go"))), "return observable.NewState(Task{}, observable.SubjectOptions[Task]{})", "return Task{}, nil", 1))
	if output, err := cli(); err != nil {
		t.Fatalf("delivery transition: %v\n%s", err, output)
	}
	if bytes.Contains(get(t, filepath.Join(dir, "web", "Shop", "Tasks", "Single.ts")), []byte("ObservableQueryFor")) {
		t.Fatal("stale observable proxy after snapshot transition")
	}
}

func TestObservableCollectionsRejectUnsupportedClientIdentityBeforePublication(t *testing.T) {
	for _, field := range []string{
		"Key string `json:\"id\" arc:\"identity\"`",
		"ID string `json:\"id\"`; Key string `json:\"key\" arc:\"identity\"`",
		"ID string `json:\"key\" arc:\"identity\"`",
		"ID *string `json:\"id\" arc:\"identity\"`",
		"ID string `json:\"id,omitempty\" arc:\"identity\"`",
		"Embedded",
	} {
		t.Run(field, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "input.go"), "//arc:namespace Shop\npackage consumer\nimport \"github.com/cratis/arc.go/observable\"\ntype Embedded struct { ID string `json:\"id\"` }\n//arc:readmodel\ntype Task struct { "+field+" }\nfunc (Task) Watch() (observable.Source[[]Task],error) { return nil,nil }\n")
			if err := Generate(t.Context(), Config{Dir: dir, TypeScriptOut: "web"}); err == nil || !strings.Contains(err.Error(), "identity") && !strings.Contains(err.Error(), "ID/Id") {
				t.Fatalf("unsupported identity accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, Filename)); !os.IsNotExist(err) {
				t.Fatal("partial adapter publication")
			}
		})
	}
}
