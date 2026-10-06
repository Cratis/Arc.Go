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
	tidyConsumer(t, dir)
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

func TestProductionCLIRealClientFixtureIndependentConsumption(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "ContractTests", "internal", "observables")
	frontend := filepath.Join("..", "..", "..", "ContractTests", "observables", "frontend")
	dir := consumer(t) // Released Fundamentals and pushed Arc pins; no workspace/replace.
	put(t, filepath.Join(dir, "generatedconsumerfixture", "model.go"), string(get(t, filepath.Join(fixture, "generatedconsumerfixture", "model.go"))))
	for _, file := range []string{"fixture.go", "signals.go", "fixture_test.go"} {
		input := string(get(t, filepath.Join(fixture, "clientfixture", file)))
		input = strings.ReplaceAll(input, "github.com/cratis/arc.go/ContractTests/internal/observables/", "example.test/consumer/")
		put(t, filepath.Join(dir, "clientfixture", file), input)
	}
	cli := func(check bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		args := []string{"run", "./cmd/arc-gen", "-dir", dir, "-typescript-out", "web"}
		if check {
			args = append(args, "-check")
		}
		cmd := exec.CommandContext(ctx, "go", append(args, "./generatedconsumerfixture")...)
		cmd.Dir, cmd.Env = "../..", append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("real fixture production CLI: %v\n%s", err, output)
		}
	}
	cli(false)
	tidyConsumer(t, dir)
	before := outputInventory(t, dir)
	cli(true)
	assertOutputInventory(t, dir, before)
	for _, file := range []string{"All.ts", "Private.ts", "Item.ts", "index.ts"} {
		if !bytes.Equal(get(t, filepath.Join(dir, "web", "Contracts", "Items", file)), get(t, filepath.Join(frontend, "Generated", "Contracts", "Items", file))) {
			t.Fatalf("checked client fixture differs from production CLI: %s", file)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pinned independent generated real fixture contracts: %v\n%s", err, output)
	}
}

func TestCompetingObservableIdentitiesRejectAllProductionPublication(t *testing.T) {
	const input = "//arc:namespace Shop\npackage consumer\nimport \"github.com/cratis/arc.go/observable\"\ntype Embedded struct { Id string `json:\"-\"` }\n//arc:readmodel\ntype Task struct { ID string `json:\"id\"`; Title string `json:\"title\"` }\nfunc (Task) Watch() (observable.Source[[]Task],error) { return nil,nil }\nfunc (Task) All() ([]Task,error) { return nil,nil }\n"
	bootstrap := consumer(t)
	put(t, filepath.Join(bootstrap, "input.go"), input)
	if err := Generate(t.Context(), Config{Dir: bootstrap, TypeScriptOut: "web"}); err != nil {
		t.Fatal(err)
	}
	tidyConsumer(t, bootstrap)
	copyFiles := copyFixtureFiles(t, bootstrap)
	for _, fields := range []string{
		"ID string `json:\"-\"`; Id string `json:\"id\"`",
		"Id string `json:\"-\"`; ID string `json:\"id\"`",
		"ID string `json:\"other\"`; Id string `json:\"id\"`",
		"Id string `json:\"other\"`; ID string `json:\"id\"`",
		"Embedded; ID string `json:\"id\"`",
	} {
		t.Run(fields, func(t *testing.T) {
			dir := consumer(t)
			// Only the identical successful output is reused. Generate below
			// loads each changed source with full production dependency metadata.
			copyFiles(t, dir)
			config := Config{Dir: dir, TypeScriptOut: "web"}
			// A valid snapshot family also changes. No adapter, proxy, barrel,
			// manifest or journal may change after analyzer identity preflight.
			changed := strings.Replace(input, "ID string `json:\"id\"`; Title string `json:\"title\"`", fields+"; Title string `json:\"title\"`; Extra string `json:\"extra\"`", 1)
			put(t, filepath.Join(dir, "input.go"), changed)
			before := outputInventory(t, dir)
			if err := Generate(t.Context(), config); err == nil || !strings.Contains(err.Error(), "unambiguous conventional identity") {
				t.Fatalf("competing runtime identity published: %v", err)
			}
			assertOutputInventory(t, dir, before)
		})
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

func TestIdentityLessObservableCollectionsPublishLikeCSharp(t *testing.T) {
	// C# ChangeSetComputor falls back to JSON-set deltas and the Arc client
	// removes/reconciles by JSON or position when items carry no id (7c1e780).
	for name, fields := range map[string]string{
		"no identity":                "Title string `json:\"title\"`",
		"unexported lowercase field": "id string; Title string `json:\"title\"`",
	} {
		t.Run(name, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "input.go"), "//arc:namespace Shop\npackage consumer\nimport \"github.com/cratis/arc.go/observable\"\n//arc:readmodel\ntype Task struct { "+fields+" }\nfunc (Task) Watch() (observable.Source[[]Task],error) { return nil,nil }\n")
			if err := Generate(t.Context(), Config{Dir: dir, TypeScriptOut: "web"}); err != nil {
				t.Fatalf("identity-less observable collection rejected: %v", err)
			}
			proxy := string(get(t, filepath.Join(dir, "web", "Shop", "Watch.ts")))
			for _, want := range []string{"extends ObservableQueryFor<Task[]>", "static useChangeStream(getKey?: (item: Task) => unknown"} {
				if !strings.Contains(proxy, want) {
					t.Fatalf("generated identity-less proxy lacks %q:\n%s", want, proxy)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, Filename)); err != nil {
				t.Fatalf("adapter not published: %v", err)
			}
		})
	}
}
