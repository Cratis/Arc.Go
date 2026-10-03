// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publication(t *testing.T) (string, string, ApplicationProfile, *Graph, []ownedOutput) {
	t.Helper()
	module, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(module, "web")
	profile := ApplicationProfile{FormatVersion: GraphVersion, Name: "test"}
	graph := &Graph{Fingerprint: "fixture", Packages: []PackageDescriptor{{GoPath: "example.test/shop"}}}
	outputs := []ownedOutput{{filepath.Join(module, Filename), []byte(Header + "package shop\n")}, {filepath.Join(root, "A.ts"), []byte(Header + "export class A {}\n")}, {filepath.Join(root, "nested", "B.ts"), []byte(Header + "export class B {}\n")}}
	return module, root, profile, graph, outputs
}
func TestOwnedPublicationInventoryAndNoWriteCheck(t *testing.T) {
	module, root, profile, graph, outputs := publication(t)
	publish := func(check bool) error {
		return publishOwned(t.Context(), module, root, profile, graph, "", outputs, check, nil)
	}
	if err := publish(true); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("check created directory", err)
	}
	if err := publish(false); err != nil {
		t.Fatal(err)
	}
	first := get(t, filepath.Join(root, manifestName))
	before, err := os.Stat(outputs[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := publish(true); err != nil {
		t.Fatal(err)
	}
	if err := publish(false); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(outputs[1].Path)
	if err != nil || before.ModTime() != after.ModTime() {
		t.Fatal("unchanged output rewritten", err)
	}
	put(t, filepath.Join(root, "user", "keep.ts"), "export const keep = true;\n")
	stale := outputs[2].Path
	outputs = outputs[:2]
	if err := publish(true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal(err)
	}
	if !bytes.Equal(first, get(t, filepath.Join(root, manifestName))) || len(get(t, stale)) == 0 {
		t.Fatal("check mutated inventory")
	}
	if err := publish(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if string(get(t, filepath.Join(root, "user", "keep.ts"))) != "export const keep = true;\n" {
		t.Fatal("deleted user file")
	}
	if err := os.Remove(outputs[1].Path); err != nil {
		t.Fatal(err)
	}
	if err := publish(true); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatal(err)
	}
}
func TestOwnedPublicationRejectsEditedAndUnownedFilesBeforeWrites(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "stale"}[stale], func(t *testing.T) {
			module, root, profile, graph, outputs := publication(t)
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err != nil {
				t.Fatal(err)
			}
			original := get(t, outputs[0].Path)
			put(t, outputs[1].Path, Header+"// user edit\n")
			outputs[0].Content = []byte(Header + "package changed\n")
			if stale {
				outputs = append(outputs[:1], outputs[2:]...)
			}
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err == nil || !strings.Contains(err.Error(), "modified") {
				t.Fatal(err)
			}
			if !bytes.Equal(original, get(t, outputs[0].Path)) || !bytes.Contains(get(t, filepath.Join(root, "A.ts")), []byte("user edit")) {
				t.Fatal("partial write or user edit overwritten")
			}
		})
	}
}
func TestOwnedPublicationFailureJournalAndRecovery(t *testing.T) {
	for _, operation := range []string{"write", "rename", "delete", "manifest"} {
		t.Run(operation, func(t *testing.T) {
			module, root, profile, graph, outputs := publication(t)
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(root, "keep.ts"), "// handwritten\n")
			outputs[0].Content = []byte(Header + "package newer\n")
			outputs[1].Content = []byte(Header + "export class A { value = 1; }\n")
			outputs = outputs[:2]
			planted := errors.New("planted failure")
			fail := func(op, path string) error {
				if op == operation && (operation == "manifest" || strings.HasSuffix(path, ".ts")) {
					return planted
				}
				return nil
			}
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, fail); !errors.Is(err, planted) {
				t.Fatal("false success", err)
			}
			journal := get(t, filepath.Join(root, journalName))
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, true, nil); err == nil || !strings.Contains(err.Error(), "pending") {
				t.Fatal(err)
			}
			if !bytes.Equal(journal, get(t, filepath.Join(root, journalName))) {
				t.Fatal("check repaired journal")
			}
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err != nil {
				t.Fatal("recovery failed", err)
			}
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, true, nil); err != nil {
				t.Fatal(err)
			}
			if string(get(t, filepath.Join(root, "keep.ts"))) != "// handwritten\n" {
				t.Fatal("user file changed")
			}
			if _, err := os.Stat(filepath.Join(root, journalName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("journal retained after success", err)
			}
		})
	}
}
func TestOwnedPublicationRejectsEscapesAndUnsafeInventory(t *testing.T) {
	for _, kind := range []string{"root-link", "parent-link", "stale-link", "stale-parent-link", "traversal", "case", "orphan", "scope", "unsafe-parent"} {
		t.Run(kind, func(t *testing.T) {
			module, root, profile, graph, outputs := publication(t)
			if kind == "stale-link" || kind == "stale-parent-link" || kind == "scope" {
				if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err != nil {
					t.Fatal(err)
				}
			}
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "keep")
			put(t, sentinel, "untouched")
			switch kind {
			case "root-link":
				if err := os.Symlink(outside, root); err != nil {
					t.Fatal(err)
				}
			case "parent-link":
				put(t, filepath.Join(root, "keep"), "user")
				if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
					t.Fatal(err)
				}
			case "stale-link":
				if err := os.Remove(outputs[2].Path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sentinel, outputs[2].Path); err != nil {
					t.Fatal(err)
				}
				outputs = outputs[:2]
			case "stale-parent-link":
				if err := os.Rename(filepath.Join(root, "nested"), filepath.Join(module, "saved")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
					t.Fatal(err)
				}
				outputs = outputs[:2]
			case "traversal":
				outputs[1].Path = filepath.Join(root, "..", "escape.ts")
			case "case":
				put(t, filepath.Join(root, "a.ts"), "user")
			case "orphan":
				put(t, filepath.Join(root, "orphan.ts"), Header+"export {};\n")
			case "scope":
				graph.Packages = append(graph.Packages, PackageDescriptor{GoPath: "extra"})
			case "unsafe-parent":
				put(t, root, "user")
			}
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if string(get(t, sentinel)) != "untouched" {
				t.Fatal("escaped root")
			}
		})
	}
}
func TestOwnedPublicationCancelledBeforeAnyWrite(t *testing.T) {
	module, root, profile, graph, outputs := publication(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := publishOwned(ctx, module, root, profile, graph, "", outputs, false, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled publication created directories", err)
	}
}

func TestOwnedPublicationRecoveryPreservesInterveningEmptyFile(t *testing.T) {
	for _, target := range []string{"output", "manifest"} {
		t.Run(target, func(t *testing.T) {
			module, root, profile, graph, outputs := publication(t)
			failure := func(op, path string) error {
				if op == "write" && strings.HasSuffix(path, "B.ts") {
					return errors.New("stop after creating A.ts")
				}
				return nil
			}
			if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, failure); err == nil {
				t.Fatal("false success")
			}
			if !bytes.Equal(get(t, outputs[1].Path), outputs[1].Content) {
				t.Fatal("failure did not create A.ts")
			}
			path := outputs[1].Path
			if target == "manifest" {
				path = filepath.Join(root, manifestName)
			}
			put(t, path, "")
			before := outputInventory(t, module)
			journal := get(t, filepath.Join(root, journalName))
			err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil)
			if err == nil || !strings.Contains(err.Error(), map[string]string{"output": "edited during pending", "manifest": "manifest changed"}[target]) {
				t.Fatal("recovery accepted intervening empty file", err)
			}
			if !bytes.Equal(journal, get(t, filepath.Join(root, journalName))) {
				t.Fatal("journal changed")
			}
			assertOutputInventory(t, module, before)
			if info, err := os.Stat(path); err != nil || info.Size() != 0 {
				t.Fatal("intervening empty file removed or overwritten", err)
			}
		})
	}
}

// Inventory includes directories and bytes so failed preflight cannot hide a
// newly created directory, metadata file, or deleted stale output.
func outputInventory(t *testing.T, root string) map[string]string {
	t.Helper()
	inventory := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if item.IsDir() {
			inventory[relative] = "directory"
		} else {
			inventory[relative] = "file:" + string(get(t, path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func assertOutputInventory(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := outputInventory(t, root)
	if len(after) != len(before) {
		t.Fatalf("inventory changed: before %v, after %v", before, after)
	}
	for path, data := range before {
		if actual, exists := after[path]; !exists || actual != data {
			t.Fatalf("inventory changed at %s: before %q, after %q (exists %v)", path, data, actual, exists)
		}
	}
}

func TestOwnedPublicationRecoveryPreservesInterveningEdit(t *testing.T) {
	module, root, profile, graph, outputs := publication(t)
	failure := func(op, path string) error {
		if op == "write" && strings.HasSuffix(path, "B.ts") {
			return errors.New("stop")
		}
		return nil
	}
	if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, failure); err == nil {
		t.Fatal("false success")
	}
	put(t, outputs[1].Path, Header+"// user edit after failure\n")
	journal := get(t, filepath.Join(root, journalName))
	if err := publishOwned(t.Context(), module, root, profile, graph, "", outputs, false, nil); err == nil {
		t.Fatal("recovery overwrote user edit")
	}
	if !bytes.Equal(journal, get(t, filepath.Join(root, journalName))) || !strings.Contains(string(get(t, outputs[1].Path)), "user edit") {
		t.Fatal("recovery destroyed evidence")
	}
}
